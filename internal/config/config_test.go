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

func TestMergeNewsFeedsKeepsConfiguredAndAddsDiverseDefaults(t *testing.T) {
	custom := NewsFeed{Name: "custom", URL: "https://example.test/feed", Category: "general"}
	feeds := MergeNewsFeeds([]NewsFeed{custom, custom})
	seen := map[string]bool{}
	for _, feed := range feeds {
		if seen[feed.URL] {
			t.Fatalf("duplicate feed %q", feed.URL)
		}
		seen[feed.URL] = true
	}
	if !seen[custom.URL] {
		t.Fatal("configured feed was lost")
	}
	if !seen["https://news.yahoo.co.jp/rss/categories/business.xml"] {
		t.Fatal("Yahoo business baseline feed missing")
	}
	if !seen["https://rss.itmedia.co.jp/rss/2.0/aiplus.xml"] {
		t.Fatal("ITmedia AI baseline feed missing")
	}
}
