# tap-to-mac — Design

**Status:** Draft
**Date:** 2026-05-08
**Owner:** FeSens

## Summary

`tap-to-mac` is a macOS background daemon that runs configurable bash commands when the user physically taps the laptop. It uses the Apple Silicon accelerometer (Bosch BMI286 IMU via IOKit HID) — the same input source as [taigrr/spank](https://github.com/taigrr/spank), MIT-licensed — to detect taps, then matches multi-tap bursts against user-configured patterns and executes the associated command.

Built as a single Go binary. Patterns are either tap-counts (`count: 2`, `count: 3`, …) or *learned* templates recorded interactively. Single taps are always ignored to suppress accidental triggers.

## Goals

- Open arbitrary apps and run arbitrary shell commands by tapping the laptop, with no keyboard or mouse.
- Run unattended in the background, surviving reboots, with zero feedback by default (silent).
- Let the user create custom multi-tap rhythms via a `learn` command, no YAML editing required.
- Stay debuggable: structured logs, `doctor` and `test` subcommands, hot-reloadable config.

## Non-goals

- Cross-platform support. macOS Apple Silicon only — same hardware constraint as spank.
- Audio/visual feedback. Silent by design.
- Distinguishing tap location, intensity, or direction. Patterns are timing-only.
- Full-waveform DTW matching. Out of scope; intervals are sufficient.

## Requirements

- macOS on Apple Silicon (M2+ or M1 Pro), per spank's hardware constraint.
- `sudo` for IOKit HID access.
- Go 1.26+ to build from source.

---

## Architecture

Five units composed as a pipeline, plus glue:

```
┌─────┐    Sample    ┌──────────┐   Tap    ┌─────────┐  Burst  ┌─────────┐  Action  ┌────────┐
│ hid │ ───────────▶ │ detector │ ───────▶ │ grouper │ ──────▶ │ matcher │ ───────▶ │ runner │
└─────┘              └──────────┘          └─────────┘         └─────────┘          └────────┘
                                                                    ▲
                                                                    │ patterns
                                                                ┌────┴────┐
                                                                │ config  │
                                                                └─────────┘
```

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `hid` | Wire up `apple-silicon-accelerometer` (sensor + shm + detector); poll ring → push events to filter detector | `github.com/taigrr/apple-silicon-accelerometer` |
| `detector` | Threshold + cooldown filter on upstream events → emit `Tap{t, amplitude}` | nothing |
| `grouper` | Buffer taps into bursts based on `burst_window_ms`. Drop bursts of size 1 | nothing |
| `matcher` | Compare burst against ordered pattern list, return first match or none | `config` |
| `runner` | Exec matched action's command via `sh -c`, log result | `os/exec`, `logger` |
| `config` | Load + validate YAML, watch with `fsnotify`, hot-reload | `gopkg.in/yaml.v3`, `fsnotify` |
| `learn` | Interactive recording flow → write template JSON + append config | `detector`, `grouper` |
| `launchd` | Generate/install/remove plist | `launchctl` (subprocess) |
| `cli` | Subcommand dispatch | all of the above |
| `logger` | Structured JSON-lines log with size-based rotation | `lumberjack` |

Each pipeline unit has a single purpose, communicates via a typed channel, and is testable in isolation by feeding synthetic events.

---

## Tap detection and matching

### Detection (`detector`)

The upstream `apple-silicon-accelerometer/detector` already does high-pass filtering + STA/LTA peak detection and emits `Event{Time, Amplitude, Severity, ...}`. Our `detector` filter is intentionally thin:

- Pull new events appended to upstream's `Events[]`.
- Drop events with `Amplitude < min_amplitude`.
- After emitting a tap, enforce `cooldown_ms` lockout regardless of further upstream events.

Defaults: `min_amplitude: 0.05` (matches spank's working default; users tune their own value via `tap-to-mac calibrate`), `cooldown_ms: 200`. The cooldown is shorter than spank's because it serves a different role here — it's a per-tap debounce, not a per-action lockout. For multi-tap detection to work it must be smaller than `burst_window_ms` (otherwise no two taps can fit in one burst).

### Grouping (`grouper`)

- Maintain a current burst (a slice of `Tap`s).
- Each new tap with `now - last_tap_in_burst < burst_window_ms` extends the burst.
- When the window expires after the last tap, the burst closes and is emitted with its `intervals[]` (length = `len(taps) - 1`).
- **Bursts of size 1 are dropped** before reaching the matcher — this is how the 2-tap minimum is enforced. They are still logged as `tap_unmatched` so a user debugging "why didn't my tap fire?" can see them.

Default `burst_window_ms: 600`.

### Matching (`matcher`)

A pattern is internally:

```go
type Pattern struct {
    Name      string
    Count     int              // required tap count; always set
    Intervals []IntervalRange  // nil for count-only; otherwise len(Intervals) == Count - 1
    Command   string
    Enabled   bool
}

type IntervalRange struct{ MinMs, MaxMs int }
```

Two YAML pattern types compile to this:

- **`count: N`** → `Count = N`, `Intervals = nil`. Matches any burst of exactly `N` taps regardless of inter-tap timing.
- **`learned`** → `Intervals` loaded from the template JSON file, `Count = len(Intervals) + 1`. Matches when tap count and every interval fall within bounds.

Match procedure for a closed burst:

1. For each pattern in YAML order:
   1. If `len(burst.taps) != Count`, skip.
   2. If `Intervals` is nil, **match**.
   3. Otherwise, for each `i`, require `burst.intervals[i] ∈ [Intervals[i].MinMs, Intervals[i].MaxMs]`. All-or-nothing match.
2. First matching pattern wins. The user controls precedence via YAML order.

Recommended ordering convention (documented in the README): list specific learned patterns above generic `count: N` patterns, so a learned triple takes priority over the generic one.

### Learning algorithm (`learn`)

`sudo tap-to-mac learn <name>` runs interactively:

1. Prompt user to tap the pattern 5 times.
2. For each iteration, use `detector` + `grouper` to capture one burst. Show recorded burst (tap count + intervals) and accept/retry.
3. After 5 valid bursts of identical tap-count `N`, drop the worst outlier (the burst whose total duration deviates most from the median total).
4. For each interval position `i ∈ [0, N-2]`, compute `[min, max]` over the remaining 4 bursts, then pad outward by `±tolerance` (default 20% of midpoint).
5. Validate: stdev of each interval position < 50% of its mean. If not, warn and offer re-record.
6. Prompt for the command.
7. Write template JSON to `~/.config/tap-to-mac/templates/<name>.json`.
8. Append a tap entry to `config.yaml` (insertion point: above the first generic `count: N` pattern, so learned patterns automatically take priority).

---

## Configuration

### File: `~/.config/tap-to-mac/config.yaml`

```yaml
sensitivity:
  min_amplitude: 0.05
  cooldown_ms: 200
  burst_window_ms: 600

taps:
  - name: open-spotify
    pattern: { type: count, n: 2 }
    command: open -a Spotify
    enabled: true

  - name: lock-screen
    pattern: { type: count, n: 3 }
    command: pmset displaysleepnow

  - name: rhythm-vscode
    pattern:
      type: learned
      template: templates/rhythm-vscode.json
    command: open -a "Visual Studio Code"
```

### Template JSON: `~/.config/tap-to-mac/templates/<name>.json`

```json
{
  "name": "rhythm-vscode",
  "version": 1,
  "taps": 3,
  "intervals": [
    { "min_ms": 175, "max_ms": 218 },
    { "min_ms": 175, "max_ms": 234 }
  ],
  "tolerance": 0.20,
  "recorded_at": "2026-05-08T12:34:56Z",
  "samples": 5
}
```

### Validation

On load, the daemon (or any subcommand using config) checks:

- Tap names are unique and non-empty.
- `command` is non-empty.
- For `count` patterns: `n >= 2`.
- For `learned` patterns: template path resolves, JSON parses, `taps >= 2`, intervals length = `taps - 1`, all `min_ms <= max_ms`.
- `sensitivity.min_amplitude > 0`, `cooldown_ms > 0`, `burst_window_ms > cooldown_ms`.
- `enabled` is optional; defaults to `true`. Patterns with `enabled: false` are loaded but skipped during matching.

A bad config:

- At startup: refuse to start, print error, exit 1.
- During hot-reload: log error, keep the previously loaded config running.

### Hot-reload

`config` watches `config.yaml` and the templates directory via `fsnotify`. On change, attempts a re-load and atomic swap of the in-memory pattern list. The `matcher` reads from this atomic pointer.

### Command execution environment

The daemon runs as root (LaunchDaemon, required for IOKit HID). Commands must execute as the install user — `open -a Spotify` as root opens in the wrong GUI session. The `runner` switches identity by spawning:

```
launchctl asuser <UID> sudo -u <user> /bin/sh -c <command>
```

`launchctl asuser <UID>` is the canonical macOS mechanism for running a command in a target user's GUI session from a system-level daemon; `sudo -u` then drops privileges. The `--user` flag passed in the plist tells the daemon which UID/username to use.

Environment passed to the spawned shell:

- `cwd = $HOME` of the install user.
- `PATH = /usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin`.
- `HOME` set to the install user's home.
- `USER` set to the install user.
- `TERM` and `LANG` from launchd if present, otherwise unset.

Stdout/stderr from the spawned shell are captured into the structured log; they're not displayed anywhere.

---

## CLI surface

```
tap-to-mac run                  Foreground daemon mode (used by launchd plist).
tap-to-mac learn <name>         Interactive: record a custom pattern, save template + append to config.
tap-to-mac list                 Print configured taps in a table (name, pattern, command, enabled).
tap-to-mac test                 Foreground; print "would run: <command>" instead of executing.
tap-to-mac install              Write LaunchDaemon plist; launchctl bootstrap.
tap-to-mac uninstall            launchctl bootout; remove plist.
tap-to-mac doctor               Health check: IMU readable, config valid, daemon loaded, log dir writable; tail last 20 log entries.
tap-to-mac --version            Print build version.
```

`run`, `learn`, and `test` require root (IOKit HID). They `os.Geteuid()`-check and refuse with a clear message otherwise.

`install`, `uninstall`, `doctor`, `list` do not require root; they read config / call `launchctl`.

---

## Daemon (launchd) integration

Plist installed at `/Library/LaunchDaemons/com.fesens.tap-to-mac.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.fesens.tap-to-mac</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/tap-to-mac</string>
    <string>run</string>
    <string>--user</string>
    <string><INSTALL_USER></string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>/var/log/tap-to-mac/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/tap-to-mac/stderr.log</string>
  <key>WorkingDirectory</key><string>/var/log/tap-to-mac</string>
</dict>
</plist>
```

`tap-to-mac install`:

1. Verifies `/usr/local/bin/tap-to-mac` exists; if not, fails with instructions.
2. Captures the invoking user (`$SUDO_USER` or `$USER`) and embeds it in the plist as `--user`.
3. `mkdir -p /var/log/tap-to-mac`, chown to root.
4. Writes the plist with strict permissions (`root:wheel 0644`).
5. Runs `launchctl bootstrap system /Library/LaunchDaemons/com.fesens.tap-to-mac.plist`.
6. Verifies it loaded with `launchctl print system/com.fesens.tap-to-mac`.

`tap-to-mac uninstall`:

1. `launchctl bootout system/com.fesens.tap-to-mac` (idempotent).
2. Remove the plist.
3. Leave `/var/log/tap-to-mac/` and `~/.config/tap-to-mac/` in place.

---

## Logging

`/var/log/tap-to-mac/tap.log`, JSON lines, rotated by `lumberjack`: 10MB max per file, 5 files retained, gzip compressed.

Schema for each line:

```json
{
  "ts": "2026-05-08T12:34:56.789Z",
  "event": "tap_matched" | "tap_unmatched" | "command_started" | "command_finished" | "command_failed" | "config_reloaded" | "config_invalid",
  "burst_size": 3,
  "intervals_ms": [180, 195],
  "matched": "rhythm-vscode",
  "command": "open -a \"Visual Studio Code\"",
  "exit_code": 0,
  "duration_ms": 42,
  "stderr_excerpt": "..."
}
```

Unmatched bursts are logged at `event: tap_unmatched` so the user can see what their tap looked like and why it didn't fire.

`tap-to-mac doctor` tails the last 20 entries.

---

## Repo layout

```
tap-to-mac/
├── cmd/tap-to-mac/
│   └── main.go                     # cli entry, subcommand dispatch
├── internal/
│   ├── hid/                        # adapter to apple-silicon-accelerometer (sensor+shm+detector)
│   ├── detector/                   # amplitude + cooldown filter on upstream events
│   ├── grouper/                    # burst windowing
│   ├── matcher/                    # pattern matching
│   ├── runner/                     # exec sh -c
│   ├── config/                     # YAML load + fsnotify reload
│   ├── learn/                      # interactive learn mode
│   ├── launchd/                    # plist install/uninstall
│   └── logger/                     # lumberjack wrapper, json-lines
├── docs/superpowers/specs/
│   └── 2026-05-08-tap-to-mac-design.md   # this file
├── examples/
│   └── config.yaml                 # documented example
├── go.mod
├── go.sum
├── LICENSE                         # MIT
├── NOTICE                          # spank attribution
└── README.md
```

---

## Testing

- **`detector`** — feed synthetic `Sample` streams (sine waves, single spikes, noise) and assert tap output. No IMU needed.
- **`grouper`** — feed `Tap` streams with controlled timing; assert burst boundaries and interval calculations. Verify size-1 bursts are dropped.
- **`matcher`** — table-driven tests: `(patterns, burst) → expected_match`. Cover priority ordering, count-only matching, learned interval matching at boundaries, no-match.
- **`config`** — golden YAML fixtures (valid + invalid). Assert validation errors are precise.
- **`runner`** — `command: echo ok` and `command: false`; assert exit codes captured, stderr captured, command runs as the configured user. Mock `os/exec` where needed for hermeticity.
- **`learn`** — feed scripted bursts to a synthetic detector/grouper, drive the recorder; assert the template JSON emitted. Outlier rejection covered separately.
- **`launchd`** — generate plist; assert structure and embedded `--user` value. Skip `launchctl` calls in tests.
- **`hid`** — hardware boundary (wraps an external library); manual smoke test only via `tap-to-mac test` on a real Mac.

CI: `go vet`, `go test ./...`, `golangci-lint`. Build matrix on `darwin/arm64` only.

---

## Attribution & license

- Repo licensed MIT.
- `NOTICE` file at repo root credits taigrr/spank (MIT) as inspiration and `taigrr/apple-silicon-accelerometer` (MIT) as the dependency that does the IOKit HID work.
- README links to both, explains the lineage, thanks the upstream author.
- The dependency is pulled normally via `go.mod`; no source is vendored or copied.

---

## Open issues / future work (out of scope for v1)

- **GUI config editor** — v1 is YAML + CLI only.
- **Per-tap cooldown** — global cooldown only in v1.
- **Pattern conflict detector** — v1 just first-match-wins; could later warn at config load when two learned patterns overlap.
- ~~**Calibration command** — auto-tune `min_amplitude` based on a few sample taps.~~ Shipped post-v1 as `tap-to-mac calibrate`.
- **Variable-N learn mode** — currently requires all 5 recordings to have the same tap count. A future version could accept a small N variance and pick the modal count.
