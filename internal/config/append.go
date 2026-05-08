package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppendLearnedTap appends a `learned` tap binding to the YAML config file
// at the right position: above the first generic `count` pattern, so the
// learned (specific) pattern is matched first.
//
// The function preserves the rest of the file's formatting and comments by
// performing a textual insertion rather than a full re-marshal.
func AppendLearnedTap(configPath string, name, templateRelPath, command string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	lines := strings.Split(string(data), "\n")

	insertIdx := findCountTapIndex(lines)
	if insertIdx < 0 {
		insertIdx = len(lines)
	}

	entry := []string{
		fmt.Sprintf("  - name: %s", name),
		"    pattern:",
		"      type: learned",
		fmt.Sprintf("      template: %s", templateRelPath),
		fmt.Sprintf("    command: %s", quoteIfNeeded(command)),
	}

	merged := append([]string{}, lines[:insertIdx]...)
	merged = append(merged, entry...)
	merged = append(merged, lines[insertIdx:]...)

	tmp := configPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(strings.Join(merged, "\n")), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

// findCountTapIndex returns the line index of the `- name:` line of the
// first tap whose pattern type is `count`. Returns -1 if none.
func findCountTapIndex(lines []string) int {
	tapStart := -1
	patternIsCount := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		// Detect the start of a new tap entry: leading "- name:".
		if strings.HasPrefix(trimmed, "- name:") {
			if tapStart >= 0 && patternIsCount {
				return tapStart
			}
			tapStart = i
			patternIsCount = false
			continue
		}
		// Detect inline pattern: `pattern: { type: count, n: ... }`.
		if strings.Contains(trimmed, "type: count") {
			patternIsCount = true
		}
	}
	if tapStart >= 0 && patternIsCount {
		return tapStart
	}
	return -1
}

// quoteIfNeeded wraps a command in double quotes if it contains characters
// that would confuse YAML parsing.
func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, ":#\"'`{}[]&*!|>%@,") || strings.HasPrefix(s, " ") {
		// Escape any embedded double quotes.
		escaped := strings.ReplaceAll(s, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return s
}
