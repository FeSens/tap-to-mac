//go:build darwin

package main

import (
	"flag"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/detector"
	"github.com/FeSens/tap-to-mac/internal/grouper"
	"github.com/FeSens/tap-to-mac/internal/hid"
	"github.com/FeSens/tap-to-mac/internal/logger"
	"github.com/FeSens/tap-to-mac/internal/matcher"
	"github.com/FeSens/tap-to-mac/internal/runner"
)

func cmdRun(args []string) int {
	return runDaemon(args, false)
}

func cmdTest(args []string) int {
	return runDaemon(args, true)
}

// runDaemon is shared between `run` and `test`. dryRun=true logs instead of executing.
func runDaemon(args []string, dryRun bool) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	flagUser := fs.String("user", "", "user to run commands as (default: $SUDO_USER or $USER)")
	flagUID := fs.Int("uid", 0, "uid to use with launchctl asuser (default: looked up from --user)")
	logToStdout := fs.Bool("log-stdout", false, "log to stdout instead of /var/log/tap-to-mac/")
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
	uid := *flagUID
	if uid == 0 {
		u, err := uidFromUser(usr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		uid = u
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

	var lg *logger.Logger
	if *logToStdout {
		lg = logger.NewWriter(os.Stdout)
	} else {
		lg, err = logger.NewFile(logPath())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	defer lg.Close()

	mode := "run"
	if dryRun {
		mode = "test"
	}
	lg.Info(fmt.Sprintf("starting in %s mode for user %s (uid=%d) with %d tap(s)", mode, usr.Username, uid, len(cfg.Taps)))

	snap := config.NewSnapshot(cfg)
	stopWatch, err := config.Watch(*cfgPath, snap, func(c *config.Config) {
		_ = lg.Log(logger.Event{Event: "config_reloaded", Message: fmt.Sprintf("%d tap(s)", len(c.Taps))})
	}, func(e error) {
		_ = lg.Log(logger.Event{Event: "config_invalid", Error: e.Error()})
	})
	if err != nil {
		_ = lg.Log(logger.Event{Event: "warn", Message: "config watcher failed: " + err.Error()})
	} else {
		defer stopWatch()
	}

	ctx, cancel := newCtx()
	defer cancel()

	// In dry-run mode (`tap-to-mac test`) attach to a running daemon if
	// there is one. Real run mode is the daemon — always create.
	var src *hid.Source
	if dryRun {
		src, err = openIMU()
	} else {
		src, err = hid.Open()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer src.Close()
	src.Start(ctx)

	rn := &runner.Runner{
		UID:     uid,
		User:    usr.Username,
		HomeDir: usr.HomeDir,
		DryRun:  dryRun,
	}

	processBurst := func(b grouper.Burst) {
		ev := logger.Event{
			BurstSize:   b.Size(),
			IntervalsMs: durMs(b.Intervals),
		}
		if b.Size() < 2 {
			ev.Event = "tap_unmatched"
			ev.Message = "burst dropped: size < 2"
			_ = lg.Log(ev)
			return
		}
		match := matcher.Match(b, snap.Load().Taps)
		if match == nil {
			ev.Event = "tap_unmatched"
			_ = lg.Log(ev)
			return
		}
		ev.Event = "tap_matched"
		ev.Matched = match.Name
		ev.Command = match.Command
		_ = lg.Log(ev)

		go func(t config.Tap) {
			start := time.Now()
			res, err := rn.Run(ctx, t.Command)
			dur := time.Since(start)
			out := logger.Event{
				BurstSize:   b.Size(),
				IntervalsMs: durMs(b.Intervals),
				Matched:     t.Name,
				Command:     t.Command,
				DurationMs:  dur.Milliseconds(),
			}
			if err != nil {
				out.Event = "command_failed"
				out.Error = err.Error()
				_ = lg.Log(out)
				return
			}
			ec := res.ExitCode
			out.ExitCode = &ec
			out.StderrExcerpt = res.Stderr
			if dryRun {
				out.Event = "command_dryrun"
				out.Message = "would run: " + t.Command
			} else if ec == 0 {
				out.Event = "command_finished"
			} else {
				out.Event = "command_failed"
			}
			_ = lg.Log(out)
		}(*match)
	}

	burstWindow := snap.Load().Sensitivity.BurstWindow()
	g := grouper.New(burstWindow, processBurst)

	filter := detector.New(
		snap.Load().Sensitivity.MinAmplitude,
		snap.Load().Sensitivity.Cooldown(),
	)

	// Re-tune the filter when config reloads. Since the filter has internal
	// state we mutate fields directly.
	go func() {
		for range tickEvery(time.Second) {
			c := snap.Load()
			filter.MinAmplitude = c.Sensitivity.MinAmplitude
			filter.Cooldown = c.Sensitivity.Cooldown()
			g.Window = c.Sensitivity.BurstWindow()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			g.Flush()
			return 0
		case e := <-src.SensorError():
			fmt.Fprintln(os.Stderr, "sensor error:", e)
			return 1
		case ev, ok := <-src.Events():
			if !ok {
				return 0
			}
			if tap := filter.Process(ev.Time, ev.Amplitude); tap != nil {
				g.Add(*tap)
			}
		}
	}
}

func durMs(ds []time.Duration) []int64 {
	out := make([]int64, len(ds))
	for i, d := range ds {
		out[i] = d.Milliseconds()
	}
	return out
}

func ensureConfig(path string, usr *user.User) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return err
	}
	starter := []byte(starterConfig)
	if err := os.WriteFile(path, starter, 0o644); err != nil {
		return err
	}
	// chown to the install user if we can.
	if uid, err := strconv.Atoi(usr.Uid); err == nil {
		_ = os.Chown(path, uid, -1)
		_ = os.Chown(filepathDir(path), uid, -1)
	}
	return nil
}

func filepathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

const starterConfig = `# tap-to-mac config — auto-generated. Edit and the daemon picks it up.
# Run ` + "`sudo tap-to-mac calibrate`" + ` to tune min_amplitude to your tap strength.

sensitivity:
  min_amplitude: 0.05
  cooldown_ms: 350
  burst_window_ms: 600

taps:
  - name: open-spotify
    pattern: { type: count, n: 2 }
    command: open -a Spotify

  - name: lock-screen
    pattern: { type: count, n: 3 }
    command: pmset displaysleepnow
`

// tickEvery returns a channel ticking at d, used in place of time.Tick to
// allow goroutine teardown without leaking the timer in process exit.
func tickEvery(d time.Duration) <-chan time.Time {
	t := time.NewTicker(d)
	ch := make(chan time.Time, 1)
	go func() {
		for v := range t.C {
			ch <- v
		}
	}()
	return ch
}
