package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FeSens/tap-to-mac/internal/launchd"
)

func cmdInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	binPath := fs.String("binary", "/usr/local/bin/tap-to-mac", "path to the installed binary")
	flagUser := fs.String("user", "", "user the daemon should run commands for (default: $SUDO_USER)")
	_ = fs.Parse(args)

	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "install requires root; run with sudo")
		return 1
	}

	if _, err := os.Stat(*binPath); err != nil {
		fmt.Fprintf(os.Stderr, "binary not found at %s. Build and copy it there first:\n", *binPath)
		fmt.Fprintln(os.Stderr, "  go build -o tap-to-mac ./cmd/tap-to-mac")
		fmt.Fprintln(os.Stderr, "  sudo cp tap-to-mac "+*binPath)
		return 1
	}

	usr, err := resolveInstallUser(*flagUser)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	uid, err := uidFromUser(usr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := launchd.Install(launchd.Args{
		BinaryPath: *binPath,
		User:       usr.Username,
		UID:        uid,
		LogDir:     logDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Installed %s; will run commands as %s (uid %d).\n", launchd.Plist(), usr.Username, uid)
	return 0
}

func cmdUninstall(_ []string) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "uninstall requires root; run with sudo")
		return 1
	}
	if err := launchd.Uninstall(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Uninstalled.")
	return 0
}
