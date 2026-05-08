package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetMinAmplitude_ReplacesExistingLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `# preserved comment
sensitivity:
  min_amplitude: 0.15
  cooldown_ms: 200
  burst_window_ms: 600

taps:
  - name: a
    pattern: { type: count, n: 2 }
    command: echo
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMinAmplitude(path, 0.07); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "min_amplitude: 0.07") {
		t.Fatalf("expected updated value, got:\n%s", s)
	}
	if !strings.Contains(s, "# preserved comment") {
		t.Fatal("comment lost during rewrite")
	}
	if !strings.Contains(s, "cooldown_ms: 200") {
		t.Fatal("sibling key lost during rewrite")
	}
}

func TestSetMinAmplitude_InsertsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `sensitivity:
  cooldown_ms: 200
  burst_window_ms: 600
taps:
  - name: a
    pattern: { type: count, n: 2 }
    command: echo
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMinAmplitude(path, 0.07); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "min_amplitude: 0.07") {
		t.Fatalf("expected inserted value, got:\n%s", s)
	}
	// Verify we can still load the result.
	if _, err := Load(path); err != nil {
		t.Fatalf("rewritten file failed to parse: %v", err)
	}
}

func TestSetMinAmplitude_NoSensitivityBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `taps:
  - name: a
    pattern: { type: count, n: 2 }
    command: echo
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMinAmplitude(path, 0.07); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("rewritten file failed to parse: %v", err)
	}
}
