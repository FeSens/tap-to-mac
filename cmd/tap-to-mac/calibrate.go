//go:build darwin

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/detector"
	"github.com/FeSens/tap-to-mac/internal/hid"
)

const (
	calibrationSamples   = 5
	calibrationTimeoutS  = 30
	calibrationFloor     = 0.02 // never set min_amplitude below this
	calibrationCeiling   = 0.5  // never set min_amplitude above this
	calibrationFactor    = 0.4  // threshold = median * factor
	calibrationCooldown  = 200 * time.Millisecond
	calibrationLowFilter = 0.01 // very-low filter so even gentle taps register
)

func cmdCalibrate(args []string) int {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	flagUser := fs.String("user", "", "user (only used to derive default config path)")
	_ = fs.Parse(args)

	if err := hid.CheckRoot(os.Geteuid); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	usr, err := resolveInstallUser(*flagUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := ensureConfig(*cfgPath, usr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	src, err := openIMU()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer src.Close()
	ctx, cancel := newCtx()
	defer cancel()
	src.Start(ctx)

	threshold, err := runCalibration(ctx, src, os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := config.SetMinAmplitude(*cfgPath, threshold); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Updated %s — sensitivity.min_amplitude = %.3f\n", *cfgPath, threshold)
	return 0
}

// runCalibration prompts the user, listens for ~5 taps, and returns the
// recommended threshold. It does not update any config file.
func runCalibration(ctx context.Context, src *hid.Source, in io.Reader, out io.Writer) (float64, error) {
	rd := bufio.NewReader(in)
	fmt.Fprintf(out, "Calibration: tap your laptop firmly %d times. Press Enter to start.\n", calibrationSamples)
	if _, err := rd.ReadString('\n'); err != nil {
		// EOF (e.g. piped) is fine — just begin.
	}

	fmt.Fprintln(out, "Listening...")

	filter := detector.New(calibrationLowFilter, calibrationCooldown)
	amps := []float64{}

	overallTimer := time.NewTimer(calibrationTimeoutS * time.Second)
	defer overallTimer.Stop()

	for len(amps) < calibrationSamples {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-overallTimer.C:
			if len(amps) < 3 {
				return 0, fmt.Errorf("calibrate: only saw %d/%d taps in %ds; try again or check that you're tapping the laptop body",
					len(amps), calibrationSamples, calibrationTimeoutS)
			}
			fmt.Fprintf(out, "  (only got %d taps; computing with what we have)\n", len(amps))
			goto done
		case e := <-src.SensorError():
			return 0, fmt.Errorf("calibrate: sensor error: %w", e)
		case ev, ok := <-src.Events():
			if !ok {
				return 0, fmt.Errorf("calibrate: event stream closed")
			}
			if tap := filter.Process(ev.Time, ev.Amplitude); tap != nil {
				amps = append(amps, tap.Amplitude)
				fmt.Fprintf(out, "  tap %d: amplitude %.3f\n", len(amps), tap.Amplitude)
			}
		}
	}
done:
	threshold := recommendedThreshold(amps)
	fmt.Fprintf(out, "Median amplitude: %.3f → recommended min_amplitude: %.3f\n", median(amps), threshold)
	return threshold, nil
}

// recommendedThreshold computes the suggested min_amplitude from a list of
// observed tap amplitudes. The result is clamped to [floor, ceiling] and
// scaled by `factor` from the median.
func recommendedThreshold(amps []float64) float64 {
	if len(amps) == 0 {
		return calibrationFloor
	}
	med := median(amps)
	t := med * calibrationFactor
	if t < calibrationFloor {
		t = calibrationFloor
	}
	if t > calibrationCeiling {
		t = calibrationCeiling
	}
	return roundTo(t, 3)
}

func median(xs []float64) float64 {
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	n := len(cp)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

func roundTo(v float64, decimals int) float64 {
	mult := math.Pow10(decimals)
	return math.Round(v*mult) / mult
}

// promptYesNo asks the user a yes/no question with a default. Returns true
// for yes. EOF or empty input returns the default.
func promptYesNo(in io.Reader, out io.Writer, question string, dflt bool) bool {
	suffix := " [Y/n] "
	if !dflt {
		suffix = " [y/N] "
	}
	rd := bufio.NewReader(in)
	fmt.Fprint(out, question+suffix)
	line, err := rd.ReadString('\n')
	if err != nil {
		return dflt
	}
	resp := strings.ToLower(strings.TrimSpace(line))
	if resp == "" {
		return dflt
	}
	return resp == "y" || resp == "yes"
}
