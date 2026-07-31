// Package install manages the always-on service that runs radio-dj in the
// background, surviving reboots and terminal closes.
//
//	macOS  → launchd user agent   (~/Library/LaunchAgents/com.radio-dj.plist)
//	Linux  → systemd user unit    (~/.config/systemd/user/radio-dj.service)
package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const label = "com.radio-dj"

// StableBinPath is where the binary is copied so the service survives a
// repo move/delete.
func StableBinPath() string {
	return filepath.Join(os.Getenv("HOME"), ".local", "bin", "radio-dj")
}

// Install copies the running binary to a stable path, then registers the
// always-on service. If bin is "" the currently-running executable is used.
func Install(bin string) error {
	if bin == "" {
		b, err := os.Executable()
		if err != nil {
			return err
		}
		bin = b
	}
	stable := StableBinPath()
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(stable), err)
	}
	if bin != stable {
		if err := copyFile(bin, stable); err != nil {
			return fmt.Errorf("install binary to %s: %w", stable, err)
		}
		postCopy(stable) // macOS: ad-hoc re-sign; Linux: no-op
		fmt.Printf("✓ binary installed → %s\n", stable)
		bin = stable
	}
	if err := installService(bin); err != nil {
		return err
	}
	fmt.Printf("✓ radio-dj running in background (%s)\n", label)
	fmt.Printf("  UI:       http://localhost:7710\n  stream:   http://localhost:7702/stream.mp3\n")
	fmt.Printf("  logs:     %s/radio-dj.{out,err}.log\n  uninstall: %s uninstall\n", stateDir(), stable)
	return nil
}

// Uninstall stops and removes the always-on service (and its config file).
func Uninstall() error {
	return uninstallService()
}

// copyFile copies src to dst with mode 0755.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// envVars returns PATH + all RDJ_* and ZAI_API_KEY from the current process
// env, so keys/config ride with the service. launchd and systemd user units
// both start with a minimal environment; without this the service can't find
// ffmpeg, edge-tts, or your API keys.
func envVars() map[string]string {
	vars := map[string]string{}
	if path := os.Getenv("PATH"); path != "" {
		vars["PATH"] = path
	}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "RDJ_") || strings.HasPrefix(e, "ZAI_API_KEY") {
			kv := strings.SplitN(e, "=", 2)
			if len(kv) == 2 {
				vars[kv[0]] = kv[1]
			}
		}
	}
	return vars
}

// stateDir is the standalone state directory (~/.radio-dj).
func stateDir() string {
	return os.Getenv("HOME") + "/.radio-dj"
}
