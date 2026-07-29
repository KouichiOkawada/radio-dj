package radio

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"radio-dj/internal/config"
	"radio-dj/internal/status"
)

// PrintNow prints the persisted now-playing state (written by `serve`).
// Reads the state file so the `now` command works as a separate process.
func PrintNow(cfg config.Config) {
	b, err := os.ReadFile(filepath.Join(cfg.StateDir, "now-playing.json"))
	if err != nil {
		fmt.Println("(no now-playing state — is `radio-dj serve` running?)")
		return
	}
	var np status.NowPlaying
	if err := json.Unmarshal(b, &np); err != nil {
		fmt.Println("now-playing: (unparseable)", string(b))
		return
	}
	if np.Current.Title == "" {
		fmt.Println("(nothing on air yet)")
	} else {
		out := np.Current.Title
		if np.Current.Artist != "" {
			out += " — " + np.Current.Artist
		}
		fmt.Println("now playing:", out)
	}
	printAccess(cfg)
}

// printAccess shows the stream/admin/request endpoints so icecast is reachable
// without digging through icecast.xml for the admin password.
func printAccess(cfg config.Config) {
	host := cfg.IcecastHost
	if host == "" {
		host = "localhost"
	}
	fmt.Println()
	fmt.Printf("stream:   http://%s:%d%s\n", host, cfg.IcecastPort, cfg.IcecastMount)
	if pw := readTag(filepath.Join(cfg.StateDir, "icecast.xml"), "<admin-password>", "</admin-password>"); pw != "" {
		fmt.Printf("admin:    http://%s:%d/admin  (admin/%s)\n", host, cfg.IcecastPort, pw)
	}
	fmt.Printf("request:  http://%s:%d/request\n", host, cfg.StatusPort)
	if ip := tailscaleIP(); ip != "" {
		fmt.Println()
		fmt.Printf("tailscale (para apps de radio / otros dispositivos):\n")
		fmt.Printf("  stream:  http://%s:%d%s\n", ip, cfg.IcecastPort, cfg.IcecastMount)
		fmt.Printf("  request: http://%s:%d/request\n", ip, cfg.StatusPort)
	}
}

// tailscaleIP returns the node's Tailscale IPv4 (empty if Tailscale is absent
// or not running). Surfaced so radio apps on the tailnet get a reachable URL.
func tailscaleIP() string {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readTag extracts the content of an XML tag from a file (empty if missing).
func readTag(path, openTag, closeTag string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.Index(s, openTag)
	if i < 0 {
		return ""
	}
	s = s[i+len(openTag):]
	j := strings.Index(s, closeTag)
	if j < 0 {
		return ""
	}
	return s[:j]
}
