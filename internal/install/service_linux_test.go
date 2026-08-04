//go:build linux

package install

import (
	"strings"
	"testing"
)

// TestInitSystemIsKnown just guards against a panic and an unexpected value;
// the result is host-dependent.
func TestInitSystemIsKnown(t *testing.T) {
	switch got := initSystem(); got {
	case "openrc", "systemd", "":
		// ok
	default:
		t.Fatalf("initSystem() = %q, want one of openrc/systemd/\"\"", got)
	}
}

func TestOpenRCUnitContent(t *testing.T) {
	got := openrcUnitContent("/home/rider/.local/bin/radio-dj", "rider", "/home/rider")
	for _, w := range []string{
		"#!/sbin/openrc-run",
		`command="/home/rider/.local/bin/radio-dj"`,
		`command_args="serve"`,
		`command_user="rider"`,
		"command_background=true",
		`pidfile="/run/${RC_SVCNAME}.pid"`,
		`output_log="/home/rider/.radio-dj/radio-dj.out.log"`,
		"depend() {",
		"need net",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("unit content missing %q\n--- got ---\n%s", w, got)
		}
	}
}

func TestOpenRCConfContent(t *testing.T) {
	got := openrcConfContent("/home/rider")
	if !strings.Contains(got, `HOME="/home/rider"`) {
		t.Errorf("conf missing HOME; got:\n%s", got)
	}
	wantPath := `PATH="/home/rider/.local/bin:/home/rider/.radio-dj/venv/bin:/usr/local/bin:/usr/bin:/bin"`
	if !strings.Contains(got, wantPath) {
		t.Errorf("conf missing venv PATH;\nwant: %s\n got:\n%s", wantPath, got)
	}
}
