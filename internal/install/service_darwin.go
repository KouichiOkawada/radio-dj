//go:build darwin

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// postCopy re-signs the binary ad-hoc. macOS attaches com.apple.provenance to
// a go-built binary copied to a new path, and the resulting async validation
// hangs every shell invocation (launchd-launched serve may survive, but any
// user-shell invocation like `radio-dj now` blocks until killed). Re-signing
// clears it. Best-effort (codesign absent → skip).
func postCopy(bin string) {
	_ = exec.Command("codesign", "-s", "-", "--force", bin).Run()
}

// installService writes the launchd agent plist and loads it.
func installService(bin string) error {
	plist, err := plistPath()
	if err != nil {
		return err
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s/radio-dj.out.log</string>
  <key>StandardErrorPath</key><string>%s/radio-dj.err.log</string>
  %s
</dict>
</plist>
`, label, bin, stateDir(), stateDir(), envBlock())
	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	// unload if already loaded, then load fresh
	_, _ = exec.Command("launchctl", "unload", plist).CombinedOutput()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	return nil
}

// uninstallService unloads and removes the launchd agent.
func uninstallService() error {
	plist, err := plistPath()
	if err != nil {
		return err
	}
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		fmt.Printf("(unload: %v)\n", err)
	}
	_ = os.Remove(plist)
	fmt.Printf("✓ radio-dj uninstalled (launchd %s removed)\n", label)
	return nil
}

func plistPath() (string, error) {
	home := os.Getenv("HOME")
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, label+".plist"), nil
}

// envBlock renders the launchd EnvironmentVariables dict from envVars().
func envBlock() string {
	vars := envVars()
	if len(vars) == 0 {
		return ""
	}
	var lines []string
	for k, v := range vars {
		lines = append(lines, fmt.Sprintf("<key>%s</key><string>%s</string>", k, v))
	}
	return "<key>EnvironmentVariables</key>\n  <dict>\n    " +
		strings.Join(lines, "\n    ") + "\n  </dict>"
}
