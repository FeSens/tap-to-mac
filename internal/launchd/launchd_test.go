package launchd

import (
	"strings"
	"testing"
)

func TestRenderPlist_EmbedsValues(t *testing.T) {
	xml, err := RenderPlist(Args{
		BinaryPath: "/usr/local/bin/tap-to-mac",
		User:       "alice",
		UID:        501,
		LogDir:     "/var/log/tap-to-mac",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"com.fesens.tap-to-mac",
		"<string>/usr/local/bin/tap-to-mac</string>",
		"<string>alice</string>",
		"<string>501</string>",
		"<string>/var/log/tap-to-mac/stdout.log</string>",
		"<string>/var/log/tap-to-mac/stderr.log</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("plist missing %q\n---\n%s", want, xml)
		}
	}
}
