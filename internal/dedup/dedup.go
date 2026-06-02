// Package dedup provides an in-memory TTL set used to collapse repeated views
// from the same visitor within a configurable window.
package dedup

import (
	"sync"
	"time"
)

type Dedup struct {
	mu     sync.Mutex
	seen   map[string]time.Time // key -> expiry
	window time.Duration
	stop   chan struct{}
}

func New(window time.Duration) *Dedup {
	d := &Dedup{
		seen:   make(map[string]time.Time),
		window: window,
		stop:   make(chan struct{}),
	}
	go d.gcLoop()
	return d
}

// Seen records key and reports whether it was already present (i.e. a
// duplicate) within the window.
func (d *Dedup) Seen(key string) bool {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if exp, ok := d.seen[key]; ok && now.Before(exp) {
		return true
	}
	d.seen[key] = now.Add(d.window)
	return false
}

func (d *Dedup) gcLoop() {
	ticker := time.NewTicker(max(d.window, time.Minute))
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			now := time.Now()
			d.mu.Lock()
			for k, exp := range d.seen {
				if now.After(exp) {
					delete(d.seen, k)
				}
			}
			d.mu.Unlock()
		}
	}
}

func (d *Dedup) Stop() { close(d.stop) }
