package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `taps:
  - name: a
    pattern: { type: count, n: 2 }
    command: echo
`)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sensitivity.MinAmplitude != DefaultMinAmplitude {
		t.Fatalf("min_amplitude default not applied: %v", cfg.Sensitivity.MinAmplitude)
	}
	if cfg.Sensitivity.CooldownMs != DefaultCooldownMs {
		t.Fatalf("cooldown default: %v", cfg.Sensitivity.CooldownMs)
	}
	if cfg.Sensitivity.BurstWindowMs != DefaultBurstWindowMs {
		t.Fatalf("burst_window default: %v", cfg.Sensitivity.BurstWindowMs)
	}
	if !cfg.Taps[0].Enabled {
		t.Fatal("enabled should default to true")
	}
}

func TestLoad_BurstWindowMustExceedCooldown(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `sensitivity:
  cooldown_ms: 700
  burst_window_ms: 600
taps:
  - name: a
    pattern: { type: count, n: 2 }
    command: echo
`)
	if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "burst_window_ms") {
		t.Fatalf("expected burst_window_ms validation error, got %v", err)
	}
}

func TestLoad_DuplicateNameRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `taps:
  - name: dup
    pattern: { type: count, n: 2 }
    command: echo a
  - name: dup
    pattern: { type: count, n: 3 }
    command: echo b
`)
	if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}

func TestLoad_DisabledLearnedSkipsTemplate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `taps:
  - name: a
    enabled: false
    pattern:
      type: learned
      template: templates/missing.json
    command: echo
`)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("disabled learned with missing template should still load: %v", err)
	}
	if cfg.Taps[0].Enabled {
		t.Fatal("expected enabled=false")
	}
}

func TestLoad_CountNMustBeAtLeast2(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `taps:
  - name: a
    pattern: { type: count, n: 1 }
    command: echo
`)
	if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "n must be >= 2") {
		t.Fatalf("expected n>=2 error, got %v", err)
	}
}

func TestAppendLearnedTap_InsertsAboveFirstCount(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `taps:
  - name: open-spotify
    pattern: { type: count, n: 2 }
    command: open -a Spotify
`)
	if err := AppendLearnedTap(cfgPath, "rhythm", "templates/rhythm.json", "open -a Foo"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(cfgPath)
	got := string(body)
	rhythmIdx := strings.Index(got, "name: rhythm")
	spotifyIdx := strings.Index(got, "name: open-spotify")
	if rhythmIdx < 0 || spotifyIdx < 0 {
		t.Fatalf("entries missing:\n%s", got)
	}
	if rhythmIdx >= spotifyIdx {
		t.Fatalf("rhythm should appear before open-spotify:\n%s", got)
	}
}
