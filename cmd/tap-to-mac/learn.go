//go:build darwin

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/detector"
	"github.com/FeSens/tap-to-mac/internal/hid"
	"github.com/FeSens/tap-to-mac/internal/learn"
)

func cmdLearn(args []string) int {
	fs := flag.NewFlagSet("learn", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	flagUser := fs.String("user", "", "user (only used to derive default config path)")
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
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	src, err := hid.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer src.Close()
	ctx, cancel := newCtx()
	defer cancel()
	src.Start(ctx)

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

func (s *hidTapStream) Next(ctx context.Context) (detector.Tap, error) {
	for {
		select {
		case <-ctx.Done():
			return detector.Tap{}, ctx.Err()
		case e := <-s.src.SensorError():
			return detector.Tap{}, e
		case ev, ok := <-s.src.Events():
			if !ok {
				return detector.Tap{}, io.EOF
			}
			if tap := s.filter.Process(ev.Time, ev.Amplitude); tap != nil {
				return *tap, nil
			}
		}
	}
}
