package learn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/FeSens/tap-to-mac/internal/detector"
)

// scriptedStream feeds taps with controlled timing.
type scriptedStream struct {
	taps []detector.Tap
	idx  int
}

func (s *scriptedStream) Next(_ context.Context) (detector.Tap, error) {
	if s.idx >= len(s.taps) {
		return detector.Tap{}, io.EOF
	}
	t := s.taps[s.idx]
	s.idx++
	return t, nil
}

// fakeNow returns the time of the next tap or, if none remain, far in the
// future so that grouper.CloseAt fires.
func fakeNowFromIdx(taps []detector.Tap, idx *int) func() time.Time {
	return func() time.Time {
		if *idx >= len(taps) {
			return time.Unix(9999, 0)
		}
		return taps[*idx].Time
	}
}

// makeBurstSequence constructs a slice of taps for n bursts of size taps each.
// Bursts are separated by 2*window to ensure they close.
func makeBurstSequence(nBursts, sizeEach int, intervalsMs []int64, window time.Duration) []detector.Tap {
	out := []detector.Tap{}
	t := time.Unix(0, 0)
	for b := 0; b < nBursts; b++ {
		out = append(out, detector.Tap{Time: t, Amplitude: 0.5})
		for _, iv := range intervalsMs[:sizeEach-1] {
			t = t.Add(time.Duration(iv) * time.Millisecond)
			out = append(out, detector.Tap{Time: t, Amplitude: 0.5})
		}
		t = t.Add(2 * window)
	}
	return out
}

func TestRecorder_HappyPath(t *testing.T) {
	window := 600 * time.Millisecond
	taps := makeBurstSequence(requiredSamples, 3, []int64{200, 200}, window)

	stream := &scriptedStream{taps: taps}
	out := &bytes.Buffer{}
	in := strings.NewReader("\nopen -a Spotify\n") // Enter to start, then command

	idx := &stream.idx
	r := &Recorder{
		Stream:      stream,
		BurstWindow: window,
		In:          in,
		Out:         out,
		Tolerance:   0.20,
		NowFn:       fakeNowFromIdx(taps, idx),
	}

	res, err := r.Run(context.Background(), "test-pattern")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Command != "open -a Spotify" {
		t.Fatalf("command: %q", res.Command)
	}
	if res.Template.Taps != 3 {
		t.Fatalf("taps: %d", res.Template.Taps)
	}
	if len(res.Template.Intervals) != 2 {
		t.Fatalf("intervals: %d", len(res.Template.Intervals))
	}
	for i, iv := range res.Template.Intervals {
		if iv.MinMs > 200 || iv.MaxMs < 200 {
			t.Fatalf("interval %d (%d-%d) does not contain 200ms", i, iv.MinMs, iv.MaxMs)
		}
	}
}

func TestRecorder_TooInconsistentReturnsError(t *testing.T) {
	window := 600 * time.Millisecond
	// Wildly varying intervals
	bursts := [][]int64{
		{100, 100},
		{100, 100},
		{500, 500}, // dropped as outlier — fine
		{100, 100},
		{100, 100},
	}
	taps := []detector.Tap{}
	tnow := time.Unix(0, 0)
	for _, ivs := range bursts {
		taps = append(taps, detector.Tap{Time: tnow})
		for _, iv := range ivs {
			tnow = tnow.Add(time.Duration(iv) * time.Millisecond)
			taps = append(taps, detector.Tap{Time: tnow})
		}
		tnow = tnow.Add(2 * window)
	}

	stream := &scriptedStream{taps: taps}
	out := &bytes.Buffer{}
	in := strings.NewReader("\nfoo\n")
	r := NewRecorder(stream, window, in, out)
	r.NowFn = fakeNowFromIdx(taps, &stream.idx)

	// First three bursts (100,100), (100,100), (500,500) → after dropping
	// the 500/500 outlier the remaining four converge. Should succeed.
	res, err := r.Run(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success after outlier drop: %v", err)
	}
	if res.Template.Taps != 3 {
		t.Fatalf("taps: %d", res.Template.Taps)
	}
}

func TestRecorder_StopsOnStreamError(t *testing.T) {
	window := 600 * time.Millisecond
	stream := &scriptedStream{taps: nil}
	out := &bytes.Buffer{}
	in := strings.NewReader("\n")
	r := NewRecorder(stream, window, in, out)
	r.NowFn = func() time.Time { return time.Unix(0, 0) }

	_, err := r.Run(context.Background(), "p")
	if err == nil {
		t.Fatal("expected error from empty stream")
	}
	if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("expected EOF-derived error, got %v", err)
	}
}
