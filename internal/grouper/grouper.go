// Package grouper buffers Tap events into Bursts that close after a window
// of silence following the last tap.
package grouper

import (
	"time"

	"github.com/FeSens/tap-to-mac/internal/detector"
)

// Burst is a closed group of taps separated by less than BurstWindow.
type Burst struct {
	Taps      []detector.Tap
	Intervals []time.Duration // length = len(Taps) - 1
}

// Size returns the number of taps in the burst.
func (b Burst) Size() int { return len(b.Taps) }

// Grouper accumulates taps and emits Bursts via a callback.
type Grouper struct {
	Window time.Duration
	OnBurst func(Burst)

	current []detector.Tap
	timer   *time.Timer
}

// New constructs a Grouper. emit is invoked when a burst closes.
func New(window time.Duration, emit func(Burst)) *Grouper {
	return &Grouper{Window: window, OnBurst: emit}
}

// Add appends a tap to the current burst. If the timer is running it is
// reset so that the burst stays open until window of silence after the
// last tap. Add is *not* safe for concurrent use; call from a single
// goroutine.
func (g *Grouper) Add(t detector.Tap) {
	g.current = append(g.current, t)
	if g.timer != nil {
		g.timer.Stop()
	}
	g.timer = time.AfterFunc(g.Window, g.flush)
}

// Flush forces the current burst (if any) to close immediately. Used at
// shutdown and in tests.
func (g *Grouper) Flush() {
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	g.flush()
}

func (g *Grouper) flush() {
	if len(g.current) == 0 {
		return
	}
	burst := build(g.current)
	g.current = nil
	if g.OnBurst != nil {
		g.OnBurst(burst)
	}
}

func build(taps []detector.Tap) Burst {
	intervals := make([]time.Duration, 0, len(taps)-1)
	for i := 1; i < len(taps); i++ {
		intervals = append(intervals, taps[i].Time.Sub(taps[i-1].Time))
	}
	return Burst{Taps: taps, Intervals: intervals}
}

// SyncGrouper is a deterministic, timer-free variant for tests and for
// learn-mode where we want to drive burst closure manually.
type SyncGrouper struct {
	Window time.Duration

	current []detector.Tap
}

// NewSync constructs a SyncGrouper.
func NewSync(window time.Duration) *SyncGrouper {
	return &SyncGrouper{Window: window}
}

// Add appends a tap. If the new tap arrives more than Window after the
// previous tap, the previous burst is closed and returned (with closed=true).
// The new tap then begins the next burst.
func (g *SyncGrouper) Add(t detector.Tap) (closed Burst, ok bool) {
	if len(g.current) > 0 {
		gap := t.Time.Sub(g.current[len(g.current)-1].Time)
		if gap > g.Window {
			closed = build(g.current)
			g.current = []detector.Tap{t}
			return closed, true
		}
	}
	g.current = append(g.current, t)
	return Burst{}, false
}

// CloseAt closes any pending burst as if Window had elapsed at `now`.
func (g *SyncGrouper) CloseAt(now time.Time) (Burst, bool) {
	if len(g.current) == 0 {
		return Burst{}, false
	}
	last := g.current[len(g.current)-1].Time
	if now.Sub(last) <= g.Window {
		return Burst{}, false
	}
	b := build(g.current)
	g.current = nil
	return b, true
}

// Flush closes the pending burst regardless of timing. Used at shutdown.
func (g *SyncGrouper) Flush() (Burst, bool) {
	if len(g.current) == 0 {
		return Burst{}, false
	}
	b := build(g.current)
	g.current = nil
	return b, true
}
