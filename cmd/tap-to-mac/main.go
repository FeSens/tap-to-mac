// tap-to-mac is a macOS daemon that runs configurable shell commands when
// the user physically taps the laptop. Built on the Apple Silicon IMU.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "run":
		os.Exit(cmdRun(args))
	case "learn":
		os.Exit(cmdLearn(args))
	case "list":
		os.Exit(cmdList(args))
	case "test":
		os.Exit(cmdTest(args))
	case "install":
		os.Exit(cmdInstall(args))
	case "uninstall":
		os.Exit(cmdUninstall(args))
	case "doctor":
		os.Exit(cmdDoctor(args))
	case "-v", "--version", "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `tap-to-mac — run shell commands by tapping your MacBook

USAGE
  tap-to-mac <command> [flags]

COMMANDS
  run         Foreground daemon mode (used by launchd).
  learn NAME  Record a custom rhythm and add it to the config.
  list        Print configured taps.
  test        Like run, but prints "would run: ..." instead of executing.
  install     Write LaunchDaemon plist; load it. Requires sudo.
  uninstall   Unload + remove plist. Requires sudo.
  doctor      Health check + recent log entries.
  version     Print build version.

Most subcommands take --config <path>; the default is ~/.config/tap-to-mac/config.yaml.
`)
}

func newCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// resolveInstallUser returns the username + uid + home dir of the user that
// commands should be run as. Order:
//  1. --user flag (and --uid for run)
//  2. $SUDO_USER
//  3. $USER
func resolveInstallUser(flagUser string) (*user.User, error) {
	name := flagUser
	if name == "" {
		name = os.Getenv("SUDO_USER")
	}
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" || name == "root" {
		return nil, fmt.Errorf("could not determine non-root install user (set --user, SUDO_USER, or USER)")
	}
	return user.Lookup(name)
}

func defaultConfigPath() string {
	// Prefer SUDO_USER's home if running under sudo.
	if u, err := resolveInstallUser(""); err == nil {
		return filepath.Join(u.HomeDir, ".config", "tap-to-mac", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tap-to-mac", "config.yaml")
}

func uidFromUser(u *user.User) (int, error) {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, fmt.Errorf("invalid uid %q: %w", u.Uid, err)
	}
	return uid, nil
}

const (
	logDir  = "/var/log/tap-to-mac"
	logFile = "tap.log"
)

func logPath() string { return filepath.Join(logDir, logFile) }
