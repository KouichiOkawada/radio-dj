//go:build linux

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// postCopy is a no-op on Linux.
func postCopy(_ string) {}

// installService writes a systemd user unit and enables it.
func installService(bin string) error {
	unit, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(unit, []byte(unitContent(bin)), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	// reload + enable + start
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", label},
	} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	// enable-linger so the user service survives logout/reboot
	if out, err := exec.Command("loginctl", "enable-linger", os.Getenv("USER")).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  (loginctl enable-linger failed — service will stop on logout:\n  %s)\n", strings.TrimSpace(string(out)))
	}
	return nil
}

// uninstallService stops, disables, and removes the systemd user unit.
func uninstallService() error {
	unit, err := unitPath()
	if err != nil {
		return err
	}
	// stop + disable (ignore errors if not loaded)
	_, _ = exec.Command("systemctl", "--user", "stop", label).CombinedOutput()
	_, _ = exec.Command("systemctl", "--user", "disable", label).CombinedOutput()
	_ = os.Remove(unit)
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  (daemon-reload: %v)\n", err)
	}
	fmt.Printf("✓ radio-dj uninstalled (systemd %s removed)\n", label)
	return nil
}

func unitPath() (string, error) {
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, label+".service"), nil
}

// unitContent renders the systemd unit, baking envVars() into Environment= lines
// so the service can find ffmpeg/edge-tts and the API keys without a shell.
func unitContent(bin string) string {
	vars := envVars()
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var envLines []string
	for _, k := range keys {
		envLines = append(envLines, fmt.Sprintf(`Environment="%s=%s"`, k, vars[k]))
	}
	sd := stateDir()
	return fmt.Sprintf(`[Unit]
Description=radio-dj — 24/7 AI DJ radio
After=network.target

[Service]
Type=simple
ExecStart=%s serve
Restart=always
RestartSec=5
%s
StandardOutput=append:%s/radio-dj.out.log
StandardError=append:%s/radio-dj.err.log

[Install]
WantedBy=default.target
`, bin, strings.Join(envLines, "\n"), sd, sd)
}
