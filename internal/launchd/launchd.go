// Package launchd writes the LaunchDaemon plist and bootstraps/boots-out the
// service.
package launchd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const (
	Label    = "com.fesens.tap-to-mac"
	PlistDir = "/Library/LaunchDaemons"
)

// Plist returns the path where the daemon plist lives.
func Plist() string { return filepath.Join(PlistDir, Label+".plist") }

// Args bundles the values embedded in the plist.
type Args struct {
	BinaryPath string
	User       string
	UID        int
	LogDir     string
}

const plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{ .Label }}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{ .BinaryPath }}</string>
        <string>run</string>
        <string>--user</string>
        <string>{{ .User }}</string>
        <string>--uid</string>
        <string>{{ .UID }}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>5</integer>
    <key>StandardOutPath</key>
    <string>{{ .LogDir }}/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>{{ .LogDir }}/stderr.log</string>
    <key>WorkingDirectory</key>
    <string>{{ .LogDir }}</string>
</dict>
</plist>
`

// RenderPlist returns the plist XML for the given install args.
func RenderPlist(a Args) (string, error) {
	t, err := template.New("plist").Parse(plistTpl)
	if err != nil {
		return "", err
	}
	data := struct {
		Args
		Label string
		UID   string
	}{Args: a, Label: Label, UID: strconv.Itoa(a.UID)}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Install renders the plist, writes it to disk, then bootstraps the service.
// Caller must already be root.
func Install(a Args) error {
	xml, err := RenderPlist(a)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.LogDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", a.LogDir, err)
	}
	plistPath := Plist()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(xml), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", plistPath, err)
	}
	// Bootstrap; if already loaded, bootout first.
	_ = exec.Command("launchctl", "bootout", "system/"+Label).Run()
	cmd := exec.Command("launchctl", "bootstrap", "system", plistPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall boots-out and removes the plist. Idempotent.
func Uninstall() error {
	_ = exec.Command("launchctl", "bootout", "system/"+Label).Run()
	plistPath := Plist()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", plistPath, err)
	}
	return nil
}

// Loaded returns true if the daemon is currently loaded with launchd.
func Loaded() bool {
	out, err := exec.Command("launchctl", "print", "system/"+Label).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), Label)
}
