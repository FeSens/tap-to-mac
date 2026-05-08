package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"

	"github.com/FeSens/tap-to-mac/internal/config"
	"github.com/FeSens/tap-to-mac/internal/launchd"
)

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	tail := fs.Int("tail", 20, "tail this many recent log entries")
	_ = fs.Parse(args)

	hr := func(label string, ok bool, detail string) {
		mark := "✗"
		if ok {
			mark = "✓"
		}
		if detail == "" {
			fmt.Printf("%s %s\n", mark, label)
		} else {
			fmt.Printf("%s %s — %s\n", mark, label, detail)
		}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		hr("config valid", false, err.Error())
	} else {
		hr("config valid", true, fmt.Sprintf("%s, %d tap(s)", *cfgPath, len(cfg.Taps)))
	}

	if launchd.Loaded() {
		hr("daemon loaded", true, "launchctl print system/"+launchd.Label+" returned ok")
	} else {
		hr("daemon loaded", false, "run `sudo tap-to-mac install` to install it")
	}

	if _, err := os.Stat(logPath()); err == nil {
		hr("log file present", true, logPath())
	} else {
		hr("log file present", false, err.Error())
	}

	if os.Geteuid() == 0 {
		hr("running as root", true, "")
	} else {
		hr("running as root", false, "(not required for doctor; required for run/learn/install)")
	}

	if *tail > 0 {
		fmt.Println()
		fmt.Printf("Last %d log entries (%s):\n", *tail, logPath())
		if err := tailLog(logPath(), *tail); err != nil {
			fmt.Println("  (no entries:", err, ")")
		}
	}

	return 0
}

func tailLog(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*64), 1024*64)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	for _, l := range lines {
		fmt.Println("  " + l)
	}
	return scanner.Err()
}
