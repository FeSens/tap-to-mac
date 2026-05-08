package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/FeSens/tap-to-mac/internal/config"
)

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATTERN\tENABLED\tCOMMAND")
	for _, t := range cfg.Taps {
		fmt.Fprintf(tw, "%s\t%s\t%v\t%s\n", t.Name, describePattern(t.Pattern), t.Enabled, t.Command)
	}
	_ = tw.Flush()
	return 0
}

func describePattern(p config.Pattern) string {
	switch p.Kind {
	case config.PatternCount:
		return fmt.Sprintf("count(%d)", p.Count)
	case config.PatternLearned:
		return fmt.Sprintf("learned(%d taps, ±%.0f%%)", p.Count, p.Tolerance*100)
	default:
		return string(p.Kind)
	}
}
