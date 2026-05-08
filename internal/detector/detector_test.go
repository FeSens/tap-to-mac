package detector

import (
	"testing"
	"time"
)

func mustTap(t *testing.T, got *Tap) {
	t.Helper()
	if got == nil {
		t.Fatalf("expected tap, got nil")
	}
}

func mustNotTap(t *testing.T, got *Tap) {
	t.Helper()
	if got != nil {
		t.Fatalf("expected no tap, got %+v", got)
	}
}

func TestFilter_BelowThresholdDropped(t *testing.T) {
	f := New(0.15, 100*time.Millisecond)
	now := time.Now()
	mustNotTap(t, f.Process(now, 0.10))
}

func TestFilter_AboveThresholdEmits(t *testing.T) {
	f := New(0.15, 100*time.Millisecond)
	now := time.Now()
	got := f.Process(now, 0.20)
	mustTap(t, got)
	if got.Amplitude != 0.20 {
		t.Fatalf("amplitude: got %v want 0.20", got.Amplitude)
	}
}

func TestFilter_CooldownEnforced(t *testing.T) {
	f := New(0.15, 200*time.Millisecond)
	t0 := time.Now()
	mustTap(t, f.Process(t0, 0.20))
	mustNotTap(t, f.Process(t0.Add(100*time.Millisecond), 0.20))
	mustTap(t, f.Process(t0.Add(250*time.Millisecond), 0.20))
}

func TestFilter_FirstEventNoCooldown(t *testing.T) {
	f := New(0.15, 1*time.Second)
	got := f.Process(time.Now(), 0.20)
	mustTap(t, got)
}
