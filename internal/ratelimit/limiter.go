// Package ratelimit provides a simple per-key token-bucket limiter with
// background GC of idle keys. Sufficient for the tens-to-hundreds QPS target.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens added per second
	burst   float64 // bucket capacity
	stop    chan struct{}
}

// New returns a limiter allowing `rate` requests/sec per key with a burst
// capacity of `burst`.
func New(rate, burst float64) *Limiter {
	if burst < 1 {
		burst = 1
	}
	l := &Limiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   burst,
		stop:    make(chan struct{}),
	}
	go l.gcLoop()
	return l
}

// Allow consumes one token for key, returning false when the bucket is empty.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func (l *Limiter) gcLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			now := time.Now()
			l.mu.Lock()
			for k, b := range l.buckets {
				if now.Sub(b.last) > 10*time.Minute {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}
}

func (l *Limiter) Stop() { close(l.stop) }
