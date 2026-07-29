// Package install manages the macOS launchd agent that runs radio-dj in the
// background, always-on, surviving reboots and terminal closes — the same
// "supervisor" idea as a certain agentic OS, but self-contained.
package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const label = "com.radio-dj"

// Install writes the launchd agent and loads it. The binary registered is the
// one currently running (or the one at `bin`, if given).
func Install(bin string) error {
	if bin == "" {
		b, err := os.Executable()
		if err != nil {
			return err
		}
		bin = b
	}
	plist, err := plistPath()
	if err != nil {
		return err
	}
	env := envBlock()
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
`, label, bin, stateDir(), stateDir(), env)
	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	// unload if already loaded, then load fresh
	_, _ = exec.Command("launchctl", "unload", plist).CombinedOutput()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	fmt.Printf("✓ radio-dj instalado y corriendo en background (launchd %s)\n", label)
	fmt.Printf("  UI:      http://localhost:7710\n  stream:  http://localhost:7702/stream.mp3\n")
	fmt.Printf("  logs:    %s/radio-dj.{out,err}.log\n  uninstall: %s uninstall\n", stateDir(), bin)
	return nil
}

// Uninstall stops and removes the launchd agent.
func Uninstall() error {
	plist, err := plistPath()
	if err != nil {
		return err
	}
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		fmt.Printf("(unload: %v)\n", err)
	}
	_ = os.Remove(plist)
	fmt.Printf("✓ radio-dj desinstalado (launchd %s removido)\n", label)
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

// envBlock renders the launchd EnvironmentVariables dict from the current
// process env (only RDJ_* + ZAI_API_KEY, so keys/config ride with the agent).
func envBlock() string {
	var lines []string
	// Bake in PATH so launchd's minimal environment still finds ffmpeg,
	// edge-tts and any other tool radio-dj shells out to.
	if path := os.Getenv("PATH"); path != "" {
		lines = append(lines, fmt.Sprintf("<key>PATH</key><string>%s</string>", path))
	}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "RDJ_") || strings.HasPrefix(e, "ZAI_API_KEY") {
			kv := strings.SplitN(e, "=", 2)
			if len(kv) == 2 {
				lines = append(lines, fmt.Sprintf("<key>%s</key><string>%s</string>", kv[0], kv[1]))
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "<key>EnvironmentVariables</key>\n  <dict>\n    " + strings.Join(lines, "\n    ") + "\n  </dict>"
}

func stateDir() string {
	home := os.Getenv("HOME")
	if _, err := os.Stat(home + "/.tevunah"); err == nil {
		return home + "/.tevunah/radio-dj"
	}
	return home + "/.radio-dj"
}
