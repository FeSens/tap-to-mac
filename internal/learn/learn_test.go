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

// scriptedStream models a tap stream over virtual time. The tap's `Time`
// field IS the virtual time at which it occurs. When the recorder asks for
// Next with a non-zero timeout, the stream advances virtual now and either
// returns the next tap (if it falls within the deadline) or signals timeout.
type scriptedStream struct {
	taps       []detector.Tap
	idx        int
	virtualNow time.Time
}

func (s *scriptedStream) Next(_ context.Context, timeout time.Duration) (detector.Tap, bool, error) {
	if s.idx >= len(s.taps) {
		if timeout > 0 {
			s.virtualNow = s.virtualNow.Add(timeout)
			return detector.Tap{}, false, nil
		}
		return detector.Tap{}, false, io.EOF
	}
	next := s.taps[s.idx]
	if timeout > 0 {
		deadline := s.virtualNow.Add(timeout)
		if next.Time.After(deadline) {
			s.virtualNow = deadline
			return detector.Tap{}, false, nil
		}
	}
	s.virtualNow = next.Time
	s.idx++
	return next, true, nil
}

// makeBurstSequence constructs a slice of taps for n bursts of size taps each.
// Bursts are separated by 2*window so the captureBurst timeout fires.
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
	in := strings.NewReader("\nopen -a Spotify\n")

	r := &Recorder{
		Stream:      stream,
		BurstWindow: window,
		In:          in,
		Out:         out,
		Tolerance:   0.20,
		NowFn:       func() time.Time { return time.Unix(0, 0) },
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

func TestRecorder_OutlierDropped(t *testing.T) {
	window := 600 * time.Millisecond
	bursts := [][]int64{
		{100, 100},
		{100, 100},
		{500, 500}, // outlier
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
	r.NowFn = func() time.Time { return time.Unix(0, 0) }

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

func TestCaptureBurst_ClosesOnInactivityNotOnNewTap(t *testing.T) {
	window := 100 * time.Millisecond
	// Two taps at 0 and 50ms then a long pause; burst should close after window.
	stream := &scriptedStream{taps: []detector.Tap{
		{Time: time.Unix(0, 0)},
		{Time: time.Unix(0, int64(50*time.Millisecond))},
	}}
	r := NewRecorder(stream, window, strings.NewReader(""), &bytes.Buffer{})
	r.NowFn = func() time.Time { return time.Unix(0, 0) }

	burst, err := r.captureBurst(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if burst.Size() != 2 {
		t.Fatalf("size: got %d want 2", burst.Size())
	}
}
