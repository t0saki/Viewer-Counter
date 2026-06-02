package dedup

import (
	"testing"
	"time"
)

func TestSeen(t *testing.T) {
	d := New(time.Hour)
	defer d.Stop()
	if d.Seen("k") {
		t.Fatal("first Seen should be false")
	}
	if !d.Seen("k") {
		t.Fatal("second Seen should be true (duplicate)")
	}
	if d.Seen("other") {
		t.Fatal("different key should be false")
	}
}

func TestExpiry(t *testing.T) {
	d := New(10 * time.Millisecond)
	defer d.Stop()
	d.Seen("k")
	time.Sleep(20 * time.Millisecond)
	if d.Seen("k") {
		t.Fatal("entry should have expired")
	}
}
