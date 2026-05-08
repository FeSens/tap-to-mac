//go:build darwin

package main

import (
	"fmt"
	"os"

	"github.com/FeSens/tap-to-mac/internal/hid"
	"github.com/FeSens/tap-to-mac/internal/launchd"
)

// openIMU is the right way for CLI tools (watch/learn/calibrate/test) to
// get an IMU event stream. If a daemon is already running, attach to its
// shm; otherwise stand up our own sensor.
//
// `cmdRun` (the actual daemon) does NOT use this — it always wants to be
// the writer.
func openIMU() (*hid.Source, error) {
	if launchd.Loaded() {
		if src, err := hid.OpenAttached(); err == nil {
			fmt.Fprintln(os.Stderr, "(daemon running — attached to its event stream)")
			return src, nil
		}
		// Daemon is loaded but shm isn't readable; fall through and try
		// a fresh sensor. This typically only happens transiently while
		// the daemon is restarting.
	}
	return hid.Open()
}
