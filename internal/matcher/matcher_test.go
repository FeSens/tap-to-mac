package matcher

import (
	"testing"
	"time"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/detector"
	"github.com/FeSens/tap-to-mac/internal/grouper"
)

func mkBurst(intervalsMs ...int64) grouper.Burst {
	now := time.Unix(0, 0)
	taps := []detector.Tap{{Time: now}}
	for _, ms := range intervalsMs {
		now = now.Add(time.Duration(ms) * time.Millisecond)
		taps = append(taps, detector.Tap{Time: now})
	}
	intervals := make([]time.Duration, 0, len(intervalsMs))
	for _, ms := range intervalsMs {
		intervals = append(intervals, time.Duration(ms)*time.Millisecond)
	}
	return grouper.Burst{Taps: taps, Intervals: intervals}
}

func TestMatch_SizeOneNeverMatches(t *testing.T) {
	taps := []config.Tap{
		{Name: "x", Enabled: true, Command: "true",
			Pattern: config.Pattern{Kind: config.PatternCount, Count: 1}},
	}
	if Match(grouper.Burst{Taps: []detector.Tap{{}}}, taps) != nil {
		t.Fatal("size-1 burst must not match")
	}
}

func TestMatch_CountDouble(t *testing.T) {
	taps := []config.Tap{
		{Name: "double", Enabled: true, Command: "x",
			Pattern: config.Pattern{Kind: config.PatternCount, Count: 2}},
	}
	got := Match(mkBurst(150), taps)
	if got == nil || got.Name != "double" {
		t.Fatalf("expected double match, got %+v", got)
	}
}

func TestMatch_CountIgnoresTiming(t *testing.T) {
	taps := []config.Tap{
		{Name: "trip", Enabled: true, Command: "x",
			Pattern: config.Pattern{Kind: config.PatternCount, Count: 3}},
	}
	got := Match(mkBurst(50, 800), taps)
	if got == nil {
		t.Fatal("count pattern should ignore interval timing")
	}
}

func TestMatch_LearnedWithinTolerance(t *testing.T) {
	taps := []config.Tap{
		{Name: "learned", Enabled: true, Command: "x",
			Pattern: config.Pattern{
				Kind:      config.PatternLearned,
				Count:     3,
				Intervals: []config.IntervalRange{{MinMs: 150, MaxMs: 250}, {MinMs: 150, MaxMs: 250}},
			}},
	}
	if Match(mkBurst(200, 200), taps) == nil {
		t.Fatal("burst within bounds should match")
	}
	if Match(mkBurst(100, 200), taps) != nil {
		t.Fatal("first interval below min should not match")
	}
	if Match(mkBurst(200, 300), taps) != nil {
		t.Fatal("second interval above max should not match")
	}
}

func TestMatch_LearnedBeforeCount(t *testing.T) {
	taps := []config.Tap{
		{Name: "learned", Enabled: true, Command: "x",
			Pattern: config.Pattern{
				Kind:      config.PatternLearned,
				Count:     3,
				Intervals: []config.IntervalRange{{MinMs: 150, MaxMs: 250}, {MinMs: 150, MaxMs: 250}},
			}},
		{Name: "any-three", Enabled: true, Command: "y",
			Pattern: config.Pattern{Kind: config.PatternCount, Count: 3}},
	}
	got := Match(mkBurst(200, 200), taps)
	if got == nil || got.Name != "learned" {
		t.Fatalf("expected learned to win, got %+v", got)
	}
	got = Match(mkBurst(50, 800), taps)
	if got == nil || got.Name != "any-three" {
		t.Fatalf("expected any-three to win when learned doesn't match, got %+v", got)
	}
}

func TestMatch_DisabledSkipped(t *testing.T) {
	taps := []config.Tap{
		{Name: "off", Enabled: false, Command: "x",
			Pattern: config.Pattern{Kind: config.PatternCount, Count: 2}},
	}
	if Match(mkBurst(100), taps) != nil {
		t.Fatal("disabled tap must not match")
	}
}
