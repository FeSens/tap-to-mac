// Package runner executes a matched tap's bash command in the install user's
// GUI session.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// Result captures what happened when a command was run.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Runner exec's commands as the configured user via launchctl asuser.
type Runner struct {
	UID      int
	User     string
	HomeDir  string
	ExtraEnv map[string]string

	// DryRun, if true, captures the assembled command but does not execute it.
	DryRun bool

	// LastDryCommand is set when DryRun is true.
	LastDryCommand []string
}

// Run invokes `launchctl asuser <UID> sudo -u <user> /bin/sh -c <command>`
// with the configured environment, captures stdout/stderr, and returns a
// Result. Stdout/stderr are truncated to 1 KiB each in the returned Result.
//
// If dropping to the user fails (no sudo, asuser unavailable), this returns
// an error rather than running as root.
func (r *Runner) Run(ctx context.Context, command string) (Result, error) {
	args := []string{
		"asuser", strconv.Itoa(r.UID),
		"sudo", "-u", r.User,
		"-H",
		"/bin/sh", "-c", command,
	}
	if r.DryRun {
		r.LastDryCommand = append([]string{"launchctl"}, args...)
		return Result{}, nil
	}

	cmd := exec.CommandContext(ctx, "launchctl", args...)
	cmd.Env = r.envSlice()

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	res := Result{
		Stdout:   truncate(outBuf.String(), 1024),
		Stderr:   truncate(errBuf.String(), 1024),
		Duration: dur,
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("runner: launchctl asuser: %w", err)
	}
	res.ExitCode = 0
	return res, nil
}

func (r *Runner) envSlice() []string {
	env := []string{
		"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin",
		"HOME=" + r.HomeDir,
		"USER=" + r.User,
		"SHELL=/bin/sh",
	}
	for k, v := range r.ExtraEnv {
		env = append(env, k+"="+v)
	}
	return env
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
