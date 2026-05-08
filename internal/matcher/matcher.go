// Package matcher compares closed bursts against the configured patterns
// and returns the first matching tap binding.
package matcher

import (
	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/grouper"
)

// Match reports the first enabled config tap whose pattern matches the
// burst, or nil if nothing matches.
//
// Matching rules:
//   - Burst size must equal the pattern's tap count.
//   - For PatternCount: any timing is accepted.
//   - For PatternLearned: every interval must fall in [MinMs, MaxMs] of the
//     corresponding template interval.
func Match(burst grouper.Burst, taps []config.Tap) *config.Tap {
	if burst.Size() < 2 {
		return nil
	}
	for i := range taps {
		t := &taps[i]
		if !t.Enabled {
			continue
		}
		if patternMatches(t.Pattern, burst) {
			return t
		}
	}
	return nil
}

func patternMatches(p config.Pattern, b grouper.Burst) bool {
	if p.Count != b.Size() {
		return false
	}
	if p.Kind == config.PatternCount {
		return true
	}
	if len(p.Intervals) != len(b.Intervals) {
		return false
	}
	for i, iv := range p.Intervals {
		ms := b.Intervals[i].Milliseconds()
		if ms < iv.MinMs || ms > iv.MaxMs {
			return false
		}
	}
	return true
}
