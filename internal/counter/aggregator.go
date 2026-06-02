// Package counter holds the in-memory view counter with a write-behind buffer.
//
// Reads of the running total are served from memory (real-time, O(1)). Writes
// accumulate deltas under a single mutex and a background goroutine flushes
// them to the store on an interval or when the pending buffer grows large.
package counter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"viewer-counter/internal/store"
)

type flusher interface {
	FlushCounters(ctx context.Context, deltas map[store.Key]int64) error
	FlushBuckets(ctx context.Context, deltas map[store.BucketKey]int64) error
	InsertEvents(ctx context.Context, events []store.Event) error
}

type Aggregator struct {
	store        flusher
	recordEvents bool
	interval     time.Duration
	batch        int
	logger       *slog.Logger

	mu            sync.Mutex
	counterDeltas map[store.Key]int64
	bucketDeltas  map[store.BucketKey]int64
	events        []store.Event
	pending       int

	totals sync.Map // store.Key -> *int64

	flushSignal chan struct{}
	stop        chan struct{}
	wg          sync.WaitGroup
}

func New(s flusher, recordEvents bool, interval time.Duration, batch int, logger *slog.Logger) *Aggregator {
	return &Aggregator{
		store:         s,
		recordEvents:  recordEvents,
		interval:      interval,
		batch:         batch,
		logger:        logger,
		counterDeltas: make(map[store.Key]int64),
		bucketDeltas:  make(map[store.BucketKey]int64),
		flushSignal:   make(chan struct{}, 1),
		stop:          make(chan struct{}),
	}
}

// LoadTotals seeds the in-memory totals from persisted counters.
func (a *Aggregator) LoadTotals(m map[store.Key]int64) {
	for k, v := range m {
		a.totals.Store(k, &v)
	}
}

// Record applies a single view and returns the new running total.
func (a *Aggregator) Record(ev store.Event) int64 {
	k := store.Key{Site: ev.Site, Page: ev.Page}
	total := a.addTotal(k, 1)
	bk := store.BucketKey{Site: ev.Site, Page: ev.Page, Hour: ev.TS.UTC().Truncate(time.Hour)}

	a.mu.Lock()
	a.counterDeltas[k]++
	a.bucketDeltas[bk]++
	if a.recordEvents {
		a.events = append(a.events, ev)
	}
	a.pending++
	pending := a.pending
	a.mu.Unlock()

	if a.batch > 0 && pending >= a.batch {
		select {
		case a.flushSignal <- struct{}{}:
		default:
		}
	}
	return total
}

func (a *Aggregator) addTotal(k store.Key, d int64) int64 {
	v, _ := a.totals.LoadOrStore(k, new(int64))
	return atomic.AddInt64(v.(*int64), d)
}

// Total returns the current running total for a key.
func (a *Aggregator) Total(k store.Key) int64 {
	if v, ok := a.totals.Load(k); ok {
		return atomic.LoadInt64(v.(*int64))
	}
	return 0
}

// Start launches the background flush loop.
func (a *Aggregator) Start() {
	a.wg.Add(1)
	go a.loop()
}

func (a *Aggregator) loop() {
	defer a.wg.Done()
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			a.flush() // final flush
			return
		case <-ticker.C:
			a.flush()
		case <-a.flushSignal:
			a.flush()
		}
	}
}

func (a *Aggregator) flush() {
	a.mu.Lock()
	if len(a.counterDeltas) == 0 && len(a.bucketDeltas) == 0 && len(a.events) == 0 {
		a.mu.Unlock()
		return
	}
	counters := a.counterDeltas
	buckets := a.bucketDeltas
	events := a.events
	a.counterDeltas = make(map[store.Key]int64)
	a.bucketDeltas = make(map[store.BucketKey]int64)
	a.events = nil
	a.pending = 0
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	failCounters, failBuckets := false, false
	if err := a.store.FlushCounters(ctx, counters); err != nil {
		a.logger.Error("flush counters failed", "err", err)
		failCounters = true
	}
	if err := a.store.FlushBuckets(ctx, buckets); err != nil {
		a.logger.Error("flush buckets failed", "err", err)
		failBuckets = true
	}
	if len(events) > 0 {
		if err := a.store.InsertEvents(ctx, events); err != nil {
			a.logger.Error("insert events failed", "err", err, "dropped", len(events))
		}
	}

	// Best-effort retry: merge failed deltas back so totals converge once the
	// DB recovers. Events are dropped on failure (accepted inaccuracy).
	if failCounters || failBuckets {
		a.mu.Lock()
		if failCounters {
			for k, v := range counters {
				a.counterDeltas[k] += v
			}
		}
		if failBuckets {
			for k, v := range buckets {
				a.bucketDeltas[k] += v
			}
		}
		a.mu.Unlock()
	}
}

// Stop signals the flush loop to drain and waits for the final flush.
func (a *Aggregator) Stop() {
	close(a.stop)
	a.wg.Wait()
}
