//go:build darwin

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/detector"
	"github.com/FeSens/tap-to-mac/internal/hid"
	"github.com/FeSens/tap-to-mac/internal/learn"
)

func cmdLearn(args []string) int {
	fs := flag.NewFlagSet("learn", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	flagUser := fs.String("user", "", "user (only used to derive default config path)")
	skipCal := fs.Bool("no-calibrate", false, "skip the sensitivity calibration prompt")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: tap-to-mac learn <name>")
		return 2
	}
	name := fs.Arg(0)

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

	if !*skipCal && promptYesNo(os.Stdin, os.Stdout, "Calibrate tap sensitivity first?", true) {
		threshold, err := runCalibration(ctx, src, os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := config.SetMinAmplitude(*cfgPath, threshold); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Updated %s — sensitivity.min_amplitude = %.3f\n\n", *cfgPath, threshold)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	filter := detector.New(cfg.Sensitivity.MinAmplitude, cfg.Sensitivity.Cooldown())
	stream := &hidTapStream{src: src, filter: filter}

	rec := learn.NewRecorder(stream, cfg.Sensitivity.BurstWindow(), os.Stdin, os.Stdout)
	res, err := rec.Run(ctx, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := learn.SaveAndAppend(*cfgPath, res); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("Saved to %s\n", *cfgPath)
	return 0
}

// hidTapStream adapts the IMU source + filter into the learn.TapStream interface.
type hidTapStream struct {
	src    *hid.Source
	filter *detector.Filter
}

func (s *hidTapStream) Next(ctx context.Context, timeout time.Duration) (detector.Tap, bool, error) {
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return detector.Tap{}, false, ctx.Err()
		case <-timer:
			return detector.Tap{}, false, nil
		case e := <-s.src.SensorError():
			return detector.Tap{}, false, e
		case ev, ok := <-s.src.Events():
			if !ok {
				return detector.Tap{}, false, io.EOF
			}
			if tap := s.filter.Process(ev.Time, ev.Amplitude); tap != nil {
				return *tap, true, nil
			}
		}
	}
}
