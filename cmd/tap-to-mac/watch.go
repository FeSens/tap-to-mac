//go:build darwin

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/FeSens/tap-to-mac/internal/hid"
)

// cmdWatch streams every upstream IMU event with full amplitude info,
// no filtering. Useful for figuring out what threshold to use, or whether
// the IMU library sees a tap at all.
func cmdWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	minAmp := fs.Float64("min", 0, "only print events with amplitude >= this (default: print everything)")
	_ = fs.Parse(args)

	if err := hid.CheckRoot(os.Geteuid); err != nil {
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

	fmt.Println("Watching upstream IMU events. Tap your laptop. Ctrl-C to stop.")
	if *minAmp > 0 {
		fmt.Printf("(filtering events below amplitude %.3f)\n", *minAmp)
	}
	fmt.Println("TIME             AMPLITUDE  SEVERITY")

	count := 0
	maxAmp := 0.0
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\nSeen %d event(s). Max amplitude: %.3f.\n", count, maxAmp)
			return 0
		case e := <-src.SensorError():
			fmt.Fprintln(os.Stderr, "sensor error:", e)
			return 1
		case ev, ok := <-src.Events():
			if !ok {
				return 0
			}
			if ev.Amplitude < *minAmp {
				continue
			}
			count++
			if ev.Amplitude > maxAmp {
				maxAmp = ev.Amplitude
			}
			fmt.Printf("%s   %8.4f  %s\n",
				ev.Time.Format("15:04:05.000"),
				ev.Amplitude,
				ev.Severity,
			)
		}
	}
}

// silence unused import in some build paths
var _ = time.Now
