// Package learn implements the interactive recording flow that produces a
// learned pattern template and appends a tap entry to the YAML config.
package learn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/detector"
	"github.com/FeSens/tap-to-mac/internal/grouper"
)

const (
	requiredSamples    = 5
	defaultTolerance   = 0.20
	maxStdevRatio      = 0.50
	maxIterations      = 30 // total tries before giving up
)

// TapStream abstracts the source of detector.Tap events used during
// recording. A real implementation reads from the IMU; tests pass a slice.
type TapStream interface {
	Next(ctx context.Context) (detector.Tap, error)
}

// Recorder runs the interactive learning loop.
type Recorder struct {
	Stream         TapStream
	BurstWindow    time.Duration
	In             io.Reader
	Out            io.Writer
	Tolerance      float64
	NowFn          func() time.Time
}

// NewRecorder builds a Recorder with sensible defaults filled in for nil
// fields.
func NewRecorder(stream TapStream, burstWindow time.Duration, in io.Reader, out io.Writer) *Recorder {
	return &Recorder{
		Stream:      stream,
		BurstWindow: burstWindow,
		In:          in,
		Out:         out,
		Tolerance:   defaultTolerance,
		NowFn:       time.Now,
	}
}

// Result is the outcome of a successful learn session.
type Result struct {
	Template *config.Template
	Command  string
}

// Run drives the full interactive session and returns the template + command.
// Caller is responsible for writing the template to disk and updating config.
func (r *Recorder) Run(ctx context.Context, name string) (*Result, error) {
	rd := bufio.NewReader(r.In)

	fmt.Fprintf(r.Out, "Tap your pattern %d times. Press Enter to start.\n", requiredSamples)
	if _, err := rd.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	bursts := make([]grouper.Burst, 0, requiredSamples)
	tries := 0
	for len(bursts) < requiredSamples && tries < maxIterations {
		tries++
		fmt.Fprintf(r.Out, "[%d/%d] listening...", len(bursts)+1, requiredSamples)
		burst, err := r.captureBurst(ctx)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(r.Out, " ✓ recorded %d taps  intervals: %s\n", burst.Size(), formatIntervals(burst.Intervals))
		if burst.Size() < 2 {
			fmt.Fprintln(r.Out, "  too few taps; ignoring this attempt")
			continue
		}
		if len(bursts) > 0 && burst.Size() != bursts[0].Size() {
			fmt.Fprintf(r.Out, "  expected %d taps to match earlier recordings; ignoring this attempt\n", bursts[0].Size())
			continue
		}
		bursts = append(bursts, burst)
	}
	if len(bursts) < requiredSamples {
		return nil, fmt.Errorf("learn: only got %d/%d clean recordings before giving up", len(bursts), requiredSamples)
	}

	tpl, err := r.buildTemplate(name, bursts)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(r.Out, "Pattern: %d taps, intervals %s (with %.0f%% tolerance)\n",
		tpl.Taps, formatRanges(tpl.Intervals), tpl.Tolerance*100)
	fmt.Fprint(r.Out, "Command? ")
	cmd, err := rd.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, errors.New("learn: command is empty")
	}

	return &Result{Template: tpl, Command: cmd}, nil
}

// captureBurst pulls taps from the stream until a burst closes.
func (r *Recorder) captureBurst(ctx context.Context) (grouper.Burst, error) {
	g := grouper.NewSync(r.BurstWindow)
	var first detector.Tap
	for {
		tap, err := r.Stream.Next(ctx)
		if err != nil {
			return grouper.Burst{}, err
		}
		if first.Time.IsZero() {
			first = tap
		}
		if closed, ok := g.Add(tap); ok {
			// A new tap arrived after the window; the previous burst is closed.
			return closed, nil
		}
		// Check if we've waited longer than the window since the last tap by
		// asking the grouper to close at "now". This branch fires when the
		// stream is paced naturally — Add() detected no gap on this tap, so
		// we emulate the timer by examining the grouper state via CloseAt.
		closed, ok := g.CloseAt(r.NowFn())
		if ok {
			return closed, nil
		}
	}
}

func (r *Recorder) buildTemplate(name string, bursts []grouper.Burst) (*config.Template, error) {
	if len(bursts) == 0 {
		return nil, errors.New("buildTemplate: no bursts")
	}
	tapCount := bursts[0].Size()

	// Drop the worst outlier: the burst whose total duration deviates most
	// from the median total duration.
	bursts = dropWorstOutlier(bursts)

	intervalCount := tapCount - 1
	intervals := make([]config.IntervalRange, intervalCount)

	for i := 0; i < intervalCount; i++ {
		samples := make([]float64, 0, len(bursts))
		for _, b := range bursts {
			samples = append(samples, float64(b.Intervals[i].Milliseconds()))
		}
		mean, stdev := meanStdev(samples)
		if mean > 0 && stdev/mean > maxStdevRatio {
			return nil, fmt.Errorf("learn: interval %d is too inconsistent (stdev %.0f / mean %.0f); try again",
				i+1, stdev, mean)
		}
		minMs, maxMs := minMax(samples)
		mid := (minMs + maxMs) / 2
		pad := r.Tolerance * mid
		intervals[i] = config.IntervalRange{
			MinMs: int64(math.Max(0, math.Round(minMs-pad))),
			MaxMs: int64(math.Round(maxMs + pad)),
		}
	}

	return &config.Template{
		Name:       name,
		Version:    1,
		Taps:       tapCount,
		Intervals:  intervals,
		Tolerance:  r.Tolerance,
		RecordedAt: r.NowFn().UTC(),
		Samples:    len(bursts) + 1, // +1 for the dropped outlier
	}, nil
}

func dropWorstOutlier(bursts []grouper.Burst) []grouper.Burst {
	if len(bursts) <= 2 {
		return bursts
	}
	totals := make([]float64, len(bursts))
	for i, b := range bursts {
		var total time.Duration
		for _, iv := range b.Intervals {
			total += iv
		}
		totals[i] = float64(total.Milliseconds())
	}
	med := median(totals)
	worst := 0
	worstDist := math.Abs(totals[0] - med)
	for i := 1; i < len(totals); i++ {
		d := math.Abs(totals[i] - med)
		if d > worstDist {
			worst = i
			worstDist = d
		}
	}
	out := make([]grouper.Burst, 0, len(bursts)-1)
	out = append(out, bursts[:worst]...)
	out = append(out, bursts[worst+1:]...)
	return out
}

func meanStdev(xs []float64) (mean, stdev float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	var sq float64
	for _, x := range xs {
		d := x - mean
		sq += d * d
	}
	stdev = math.Sqrt(sq / float64(len(xs)))
	return
}

func minMax(xs []float64) (mn, mx float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	mn, mx = xs[0], xs[0]
	for _, x := range xs[1:] {
		if x < mn {
			mn = x
		}
		if x > mx {
			mx = x
		}
	}
	return
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

func formatIntervals(ivs []time.Duration) string {
	parts := make([]string, len(ivs))
	for i, iv := range ivs {
		parts[i] = fmt.Sprintf("%dms", iv.Milliseconds())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatRanges(rs []config.IntervalRange) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = fmt.Sprintf("%d-%dms", r.MinMs, r.MaxMs)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// SaveAndAppend writes the template to disk and appends the binding to the
// YAML config so the new pattern is active on the next reload.
func SaveAndAppend(configPath string, res *Result) error {
	tplDir := filepath.Join(filepath.Dir(configPath), "templates")
	tplPath := filepath.Join(tplDir, res.Template.Name+".json")
	if err := config.SaveTemplate(tplPath, res.Template); err != nil {
		return fmt.Errorf("save template: %w", err)
	}
	rel, err := filepath.Rel(filepath.Dir(configPath), tplPath)
	if err != nil {
		rel = tplPath
	}
	return config.AppendLearnedTap(configPath, res.Template.Name, rel, res.Command)
}
