package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNeedsSetupAllowsSavedMusicOnlyConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir}
	if !cfg.NeedsSetup() {
		t.Fatal("missing config should require setup")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg.NeedsSetup() {
		t.Fatal("saved music-only config should open the player")
	}
}
