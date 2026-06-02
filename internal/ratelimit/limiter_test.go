package ratelimit

import "testing"

func TestBurstThenDeny(t *testing.T) {
	// Effectively no refill within the test.
	l := New(0.0001, 3)
	defer l.Stop()
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.Allow("1.2.3.4") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed = %d, want 3 (burst)", allowed)
	}
	// Different key has its own bucket.
	if !l.Allow("5.6.7.8") {
		t.Fatal("new key should be allowed")
	}
}
