// Package config loads and validates the tap-to-mac YAML config.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults are applied when the YAML omits a field.
//
// CooldownMs is a per-tap lockout (debounces the IMU's ringing after a
// single physical tap registering as multiple events). BurstWindowMs is
// the silence after the last tap that closes a burst — must be larger than
// CooldownMs or two taps could never live in the same burst.
const (
	DefaultMinAmplitude  = 0.15
	DefaultCooldownMs    = 200
	DefaultBurstWindowMs = 600
)

// Sensitivity controls the IMU detection thresholds.
type Sensitivity struct {
	MinAmplitude  float64 `yaml:"min_amplitude"`
	CooldownMs    int     `yaml:"cooldown_ms"`
	BurstWindowMs int     `yaml:"burst_window_ms"`
}

// Cooldown returns CooldownMs as a duration.
func (s Sensitivity) Cooldown() time.Duration {
	return time.Duration(s.CooldownMs) * time.Millisecond
}

// BurstWindow returns BurstWindowMs as a duration.
func (s Sensitivity) BurstWindow() time.Duration {
	return time.Duration(s.BurstWindowMs) * time.Millisecond
}

// PatternKind enumerates the supported pattern types.
type PatternKind string

const (
	PatternCount   PatternKind = "count"
	PatternLearned PatternKind = "learned"
)

// Pattern is the compiled, ready-to-match form of a YAML pattern entry.
type Pattern struct {
	Kind      PatternKind
	Count     int
	Intervals []IntervalRange // nil for count-only; len = Count - 1 otherwise
	Tolerance float64
}

// IntervalRange is an inclusive [Min,Max] in milliseconds.
type IntervalRange struct {
	MinMs int64
	MaxMs int64
}

// Tap is one configured tap binding (a row in the `taps:` list).
type Tap struct {
	Name    string
	Command string
	Enabled bool
	Pattern Pattern
}

// Config is the loaded, validated configuration.
type Config struct {
	Path        string
	Sensitivity Sensitivity
	Taps        []Tap
}

// rawConfig mirrors the YAML structure for unmarshalling.
type rawConfig struct {
	Sensitivity struct {
		MinAmplitude  *float64 `yaml:"min_amplitude"`
		CooldownMs    *int     `yaml:"cooldown_ms"`
		BurstWindowMs *int     `yaml:"burst_window_ms"`
	} `yaml:"sensitivity"`
	Taps []rawTap `yaml:"taps"`
}

type rawTap struct {
	Name    string     `yaml:"name"`
	Command string     `yaml:"command"`
	Enabled *bool      `yaml:"enabled"`
	Pattern rawPattern `yaml:"pattern"`
}

type rawPattern struct {
	Type     string `yaml:"type"`
	N        int    `yaml:"n"`
	Template string `yaml:"template"`
}

// Template is the on-disk JSON shape of a learned pattern.
type Template struct {
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Taps       int             `json:"taps"`
	Intervals  []IntervalRange `json:"intervals"`
	Tolerance  float64         `json:"tolerance"`
	RecordedAt time.Time       `json:"recorded_at"`
	Samples    int             `json:"samples"`
}

// Load reads, parses, and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg := &Config{Path: path}
	cfg.Sensitivity.MinAmplitude = DefaultMinAmplitude
	cfg.Sensitivity.CooldownMs = DefaultCooldownMs
	cfg.Sensitivity.BurstWindowMs = DefaultBurstWindowMs
	if raw.Sensitivity.MinAmplitude != nil {
		cfg.Sensitivity.MinAmplitude = *raw.Sensitivity.MinAmplitude
	}
	if raw.Sensitivity.CooldownMs != nil {
		cfg.Sensitivity.CooldownMs = *raw.Sensitivity.CooldownMs
	}
	if raw.Sensitivity.BurstWindowMs != nil {
		cfg.Sensitivity.BurstWindowMs = *raw.Sensitivity.BurstWindowMs
	}

	if cfg.Sensitivity.MinAmplitude <= 0 {
		return nil, errors.New("config: sensitivity.min_amplitude must be > 0")
	}
	if cfg.Sensitivity.CooldownMs <= 0 {
		return nil, errors.New("config: sensitivity.cooldown_ms must be > 0")
	}
	if cfg.Sensitivity.BurstWindowMs <= cfg.Sensitivity.CooldownMs {
		return nil, fmt.Errorf("config: burst_window_ms (%d) must be greater than cooldown_ms (%d)",
			cfg.Sensitivity.BurstWindowMs, cfg.Sensitivity.CooldownMs)
	}

	configDir := filepath.Dir(path)
	seenNames := make(map[string]bool)
	for i, rt := range raw.Taps {
		tap, err := compileTap(rt, configDir)
		if err != nil {
			return nil, fmt.Errorf("config: taps[%d] (%q): %w", i, rt.Name, err)
		}
		if seenNames[tap.Name] {
			return nil, fmt.Errorf("config: duplicate tap name %q", tap.Name)
		}
		seenNames[tap.Name] = true
		cfg.Taps = append(cfg.Taps, tap)
	}
	return cfg, nil
}

func compileTap(rt rawTap, configDir string) (Tap, error) {
	if strings.TrimSpace(rt.Name) == "" {
		return Tap{}, errors.New("name is empty")
	}
	if strings.TrimSpace(rt.Command) == "" {
		return Tap{}, errors.New("command is empty")
	}
	enabled := true
	if rt.Enabled != nil {
		enabled = *rt.Enabled
	}

	tap := Tap{Name: rt.Name, Command: rt.Command, Enabled: enabled}

	switch PatternKind(rt.Pattern.Type) {
	case PatternCount:
		if rt.Pattern.N < 2 {
			return Tap{}, fmt.Errorf("count.n must be >= 2 (got %d)", rt.Pattern.N)
		}
		tap.Pattern = Pattern{Kind: PatternCount, Count: rt.Pattern.N}
	case PatternLearned:
		if rt.Pattern.Template == "" {
			return Tap{}, errors.New("learned.template path is empty")
		}
		tplPath := rt.Pattern.Template
		if !filepath.IsAbs(tplPath) {
			tplPath = filepath.Join(configDir, tplPath)
		}
		// Skip template validation for disabled entries so users can keep
		// stubbed-out bindings without a saved template yet.
		if !enabled {
			tap.Pattern = Pattern{Kind: PatternLearned}
			return tap, nil
		}
		tpl, err := LoadTemplate(tplPath)
		if err != nil {
			return Tap{}, fmt.Errorf("learned: %w", err)
		}
		if tpl.Taps < 2 {
			return Tap{}, fmt.Errorf("template: taps must be >= 2 (got %d)", tpl.Taps)
		}
		if len(tpl.Intervals) != tpl.Taps-1 {
			return Tap{}, fmt.Errorf("template: intervals length %d != taps-1 (%d)", len(tpl.Intervals), tpl.Taps-1)
		}
		for j, iv := range tpl.Intervals {
			if iv.MinMs > iv.MaxMs {
				return Tap{}, fmt.Errorf("template: interval[%d] min %d > max %d", j, iv.MinMs, iv.MaxMs)
			}
			if iv.MinMs < 0 {
				return Tap{}, fmt.Errorf("template: interval[%d] min %d < 0", j, iv.MinMs)
			}
		}
		tap.Pattern = Pattern{
			Kind:      PatternLearned,
			Count:     tpl.Taps,
			Intervals: tpl.Intervals,
			Tolerance: tpl.Tolerance,
		}
	case "":
		return Tap{}, errors.New("pattern.type is empty (must be count or learned)")
	default:
		return Tap{}, fmt.Errorf("pattern.type %q is invalid (must be count or learned)", rt.Pattern.Type)
	}

	return tap, nil
}

// LoadTemplate loads a learned pattern JSON file.
func LoadTemplate(path string) (*Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", path, err)
	}
	var tpl Template
	if err := json.Unmarshal(data, &tpl); err != nil {
		return nil, fmt.Errorf("template %s parse: %w", path, err)
	}
	return &tpl, nil
}

// SaveTemplate writes a learned pattern JSON to disk (atomic via tmp+rename).
func SaveTemplate(path string, tpl *Template) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tpl, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultConfigPath returns the canonical config path for a given $HOME.
func DefaultConfigPath(home string) string {
	return filepath.Join(home, ".config", "tap-to-mac", "config.yaml")
}

// DefaultTemplatesDir returns the canonical templates dir for a given $HOME.
func DefaultTemplatesDir(home string) string {
	return filepath.Join(home, ".config", "tap-to-mac", "templates")
}
