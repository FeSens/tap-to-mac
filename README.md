# tap-to-mac

Tap your MacBook to run shell commands.

`tap-to-mac` is a macOS background daemon that listens to the Apple Silicon
accelerometer and runs configurable bash commands when you tap the laptop.
Open Spotify with a double tap, lock the screen with a triple tap, summon a
specific app with a custom rhythm you teach it.

> Built on the IMU work from [taigrr/spank](https://github.com/taigrr/spank)
> and the
> [`apple-silicon-accelerometer`](https://github.com/taigrr/apple-silicon-accelerometer)
> library. Thanks taigrr.

## Requirements

- macOS on Apple Silicon (M2+ or M1 Pro). Same hardware constraint as spank.
- `sudo` (IOKit HID access).
- Go 1.26+ to build from source.

## Install

```bash
go install github.com/FeSens/tap-to-mac/cmd/tap-to-mac@latest
sudo cp "$(go env GOPATH)/bin/tap-to-mac" /usr/local/bin/tap-to-mac
sudo /usr/local/bin/tap-to-mac install
```

`install` writes a LaunchDaemon plist at
`/Library/LaunchDaemons/com.fesens.tap-to-mac.plist` and bootstraps it. From
that point on the daemon starts at boot.

## Configure

`tap-to-mac` reads `~/.config/tap-to-mac/config.yaml`:

```yaml
sensitivity:
  min_amplitude: 0.05
  cooldown_ms: 350
  burst_window_ms: 600

taps:
  - name: open-spotify
    pattern: { type: count, n: 2 }
    command: open -a Spotify

  - name: lock-screen
    pattern: { type: count, n: 3 }
    command: pmset displaysleepnow

  - name: rhythm-vscode
    pattern:
      type: learned
      template: templates/rhythm-vscode.json
    command: open -a "Visual Studio Code"
```

The config is hot-reloaded — edits take effect without restarting the daemon.

A starter `config.yaml` and templates directory are created on first run.

### Pattern types

- `count: { type: count, n: N }` — matches any burst of exactly `N` taps,
  ignoring inter-tap timing. `n` must be `>= 2`. Single taps are always
  ignored to avoid accidental triggers from typing or closing the lid.
- `learned: { type: learned, template: <path> }` — matches the recorded
  rhythm. Created with `tap-to-mac learn <name>` (see below).

Patterns are checked in YAML order. **First match wins.** Put more specific
learned patterns above generic `count: N` patterns.

### `enabled`

Each tap entry takes an optional `enabled: false` to keep it in the config
but skip it during matching.

## Calibrate sensitivity

The detection threshold (`sensitivity.min_amplitude`) needs to match how
hard you actually tap your laptop. The default is reasonable but worth
tuning for your taste:

```bash
sudo tap-to-mac calibrate
```

Tap firmly five times when prompted; the tool computes a recommended
threshold from the median amplitude and writes it back to your
`config.yaml`.

## Teach it a custom rhythm

```bash
sudo tap-to-mac learn rhythm-vscode
```

Offers a calibration pass first (recommended on first run), then walks you
through 5 recordings of the rhythm, drops the worst outlier, computes
interval bounds with 20% tolerance, and saves it to
`~/.config/tap-to-mac/templates/<name>.json`. Then prompts for the command
and inserts the entry above the first generic `count` pattern in
`config.yaml`. Use `--no-calibrate` to skip the calibration prompt.

## Subcommands

```
tap-to-mac run          Foreground daemon mode (used by launchd).
tap-to-mac learn <name> Record and save a custom rhythm.
tap-to-mac calibrate    Tune sensitivity to your tap strength.
tap-to-mac list         Print configured taps.
tap-to-mac test         Like run, but prints "would run: ..." instead of executing.
tap-to-mac install      Write LaunchDaemon plist; load it.
tap-to-mac uninstall    Unload + remove plist.
tap-to-mac doctor       Health check + tail of recent log entries.
tap-to-mac --version    Print build version.
```

`run`, `learn`, `calibrate`, and `test` need `sudo`; the others don't.

## Where things live

- Config: `~/.config/tap-to-mac/config.yaml`
- Templates: `~/.config/tap-to-mac/templates/*.json`
- Logs: `/var/log/tap-to-mac/tap.log` (JSON-lines, rotated 10MB × 5)
- Daemon plist: `/Library/LaunchDaemons/com.fesens.tap-to-mac.plist`

## Why it works the way it does

The daemon runs as **root** (LaunchDaemon, required for IOKit HID). Each
matched command is executed via:

```
launchctl asuser <UID> sudo -u <user> /bin/sh -c <command>
```

`launchctl asuser` injects the command into the user's GUI session — without
this, things like `open -a Spotify` would fail or open in the wrong place.
`sudo -u` then drops privileges so the command runs as your user, not root.

## Logs

Every tap, matched or not, lands in `/var/log/tap-to-mac/tap.log` as a JSON
line. `tap-to-mac doctor` shows the last 20.

If you tapped and nothing happened, check the log: most often the burst was
too short, fell outside a learned pattern's tolerance, or didn't pass
`min_amplitude`. The unmatched-burst log entry shows you exactly what was
captured so you can adjust.

## Building locally

```bash
git clone https://github.com/FeSens/tap-to-mac.git
cd tap-to-mac
go build ./cmd/tap-to-mac
go test ./...
```

Builds only on `darwin/arm64` — the IMU library is Darwin-only.

## License

MIT. See `LICENSE` and `NOTICE` for attribution.
