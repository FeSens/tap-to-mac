package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SetMinAmplitude rewrites the YAML config in place so that
// `sensitivity.min_amplitude` equals v. Comments and unrelated formatting
// are preserved by performing a line-level substitution.
//
// If the key is not present in the file, a `sensitivity:` block is
// inserted at the top. If it is present multiple times the first
// occurrence wins (consistent with YAML's own merging rule).
func SetMinAmplitude(path string, v float64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")

	formatted := strconv.FormatFloat(v, 'f', -1, 64)

	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "min_amplitude:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%smin_amplitude: %s", indent, formatted)
			return writeAtomic(path, strings.Join(lines, "\n"))
		}
	}

	// Key not found. Look for an existing `sensitivity:` block; insert
	// the field as the first child. If even that doesn't exist, prepend
	// a fresh block.
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "sensitivity:" {
			lines = append(lines[:i+1], append([]string{"  min_amplitude: " + formatted}, lines[i+1:]...)...)
			return writeAtomic(path, strings.Join(lines, "\n"))
		}
	}

	prepend := []string{"sensitivity:", "  min_amplitude: " + formatted, ""}
	out := append(prepend, lines...)
	return writeAtomic(path, strings.Join(out, "\n"))
}

func writeAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
