package radio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
		return
	}
	out := np.Current.Title
	if np.Current.Artist != "" {
		out += " — " + np.Current.Artist
	}
	fmt.Println("now playing:", out)
}
