// Package detector filters raw vibration events from
// `apple-silicon-accelerometer/detector` into a stream of taps that the
// user actually intended: events above an amplitude threshold, separated
// by at least cooldown_ms.
package detector

import "time"

// Tap is one filtered tap event.
type Tap struct {
	Time      time.Time
	Amplitude float64
}

// Filter is a stateful threshold + cooldown filter.
type Filter struct {
	MinAmplitude float64
	Cooldown     time.Duration

	lastEmitted time.Time
}

// New constructs a Filter.
func New(minAmplitude float64, cooldown time.Duration) *Filter {
	return &Filter{MinAmplitude: minAmplitude, Cooldown: cooldown}
}

// Process consumes one upstream event and returns a Tap if it survives the
// filter, or nil if it was dropped.
func (f *Filter) Process(t time.Time, amplitude float64) *Tap {
	if amplitude < f.MinAmplitude {
		return nil
	}
	if !f.lastEmitted.IsZero() && t.Sub(f.lastEmitted) < f.Cooldown {
		return nil
	}
	f.lastEmitted = t
	return &Tap{Time: t, Amplitude: amplitude}
}
