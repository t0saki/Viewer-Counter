package counter

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"viewer-counter/internal/store"
)

type fakeFlusher struct {
	mu       sync.Mutex
	counters map[store.Key]int64
	buckets  map[store.BucketKey]int64
	events   int
	failNext bool
}

func newFakeFlusher() *fakeFlusher {
	return &fakeFlusher{counters: map[store.Key]int64{}, buckets: map[store.BucketKey]int64{}}
}

func (f *fakeFlusher) FlushCounters(_ context.Context, d map[store.Key]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return io.ErrClosedPipe
	}
	for k, v := range d {
		f.counters[k] += v
	}
	return nil
}

func (f *fakeFlusher) FlushBuckets(_ context.Context, d map[store.BucketKey]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range d {
		f.buckets[k] += v
	}
	return nil
}

func (f *fakeFlusher) InsertEvents(_ context.Context, e []store.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events += len(e)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecordAndTotal(t *testing.T) {
	f := newFakeFlusher()
	a := New(f, true, time.Hour, 0, testLogger())
	k := store.Key{Site: "s", Page: "/p"}
	for i := 0; i < 5; i++ {
		a.Record(store.Event{Site: "s", Page: "/p", TS: time.Now()})
	}
	if got := a.Total(k); got != 5 {
		t.Fatalf("Total = %d, want 5", got)
	}
}

func TestLoadTotalsThenIncrement(t *testing.T) {
	f := newFakeFlusher()
	a := New(f, false, time.Hour, 0, testLogger())
	k := store.Key{Site: "s", Page: "/p"}
	a.LoadTotals(map[store.Key]int64{k: 100})
	a.Record(store.Event{Site: "s", Page: "/p", TS: time.Now()})
	if got := a.Total(k); got != 101 {
		t.Fatalf("Total = %d, want 101", got)
	}
}

func TestFlushPersistsDeltas(t *testing.T) {
	f := newFakeFlusher()
	a := New(f, true, time.Hour, 0, testLogger())
	for i := 0; i < 3; i++ {
		a.Record(store.Event{Site: "s", Page: "/p", TS: time.Now()})
	}
	a.flush()
	if f.counters[store.Key{Site: "s", Page: "/p"}] != 3 {
		t.Fatalf("flushed counter = %d, want 3", f.counters[store.Key{Site: "s", Page: "/p"}])
	}
	if f.events != 3 {
		t.Fatalf("flushed events = %d, want 3", f.events)
	}
}

func TestFlushRetriesOnCounterFailure(t *testing.T) {
	f := newFakeFlusher()
	a := New(f, false, time.Hour, 0, testLogger())
	a.Record(store.Event{Site: "s", Page: "/p", TS: time.Now()})

	f.failNext = true
	a.flush() // counter flush fails, delta should be re-merged
	if f.counters[store.Key{Site: "s", Page: "/p"}] != 0 {
		t.Fatalf("counter should not have persisted on failure")
	}
	a.flush() // retry succeeds
	if f.counters[store.Key{Site: "s", Page: "/p"}] != 1 {
		t.Fatalf("counter = %d, want 1 after retry", f.counters[store.Key{Site: "s", Page: "/p"}])
	}
	// Total stays correct in memory regardless of DB hiccup.
	if got := a.Total(store.Key{Site: "s", Page: "/p"}); got != 1 {
		t.Fatalf("Total = %d, want 1", got)
	}
}
