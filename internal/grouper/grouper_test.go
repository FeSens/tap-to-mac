package grouper

import (
	"testing"
	"time"

	"github.com/FeSens/tap-to-mac/internal/detector"
)

func tapAt(ms int) detector.Tap {
	return detector.Tap{Time: time.Unix(0, int64(ms)*int64(time.Millisecond)), Amplitude: 0.5}
}

func TestSyncGrouper_SingleTapStaysOpen(t *testing.T) {
	g := NewSync(600 * time.Millisecond)
	_, ok := g.Add(tapAt(0))
	if ok {
		t.Fatal("first tap should not close anything")
	}
	_, ok = g.CloseAt(time.Unix(0, 100*int64(time.Millisecond)))
	if ok {
		t.Fatal("burst should still be open within window")
	}
}

func TestSyncGrouper_ClosesAfterWindow(t *testing.T) {
	g := NewSync(600 * time.Millisecond)
	_, _ = g.Add(tapAt(0))
	_, _ = g.Add(tapAt(200))
	b, ok := g.CloseAt(time.Unix(0, 1000*int64(time.Millisecond)))
	if !ok {
		t.Fatal("burst should close after window")
	}
	if b.Size() != 2 {
		t.Fatalf("size: got %d want 2", b.Size())
	}
	if got, want := b.Intervals[0], 200*time.Millisecond; got != want {
		t.Fatalf("interval: got %v want %v", got, want)
	}
}

func TestSyncGrouper_StartsNewBurstWhenGapExceedsWindow(t *testing.T) {
	g := NewSync(600 * time.Millisecond)
	_, _ = g.Add(tapAt(0))
	_, _ = g.Add(tapAt(100))
	closed, ok := g.Add(tapAt(800))
	if !ok {
		t.Fatal("expected previous burst to close")
	}
	if closed.Size() != 2 {
		t.Fatalf("closed size: got %d want 2", closed.Size())
	}
	final, _ := g.Flush()
	if final.Size() != 1 {
		t.Fatalf("new burst size: got %d want 1", final.Size())
	}
}

func TestSyncGrouper_TripleTap(t *testing.T) {
	g := NewSync(600 * time.Millisecond)
	_, _ = g.Add(tapAt(0))
	_, _ = g.Add(tapAt(180))
	_, _ = g.Add(tapAt(370))
	b, _ := g.CloseAt(time.Unix(0, 2000*int64(time.Millisecond)))
	if b.Size() != 3 {
		t.Fatalf("size: got %d want 3", b.Size())
	}
	if len(b.Intervals) != 2 {
		t.Fatalf("intervals: got %d want 2", len(b.Intervals))
	}
}
