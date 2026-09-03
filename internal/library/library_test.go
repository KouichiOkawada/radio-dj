package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFolderSkipsDeletedOneShotAndReadsAttribution(t *testing.T) {
	root := t.TempDir()
	permanent := filepath.Join(root, "permanent")
	temporary := filepath.Join(root, "temporary")
	if err := os.MkdirAll(permanent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(permanent, "keep.mp3")
	oneShot := filepath.Join(temporary, "one.mp3")
	for _, path := range []string{keep, oneShot} {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sidecar := filepath.Join(temporary, "one.license.json")
	if err := os.WriteFile(sidecar, []byte(`{"source":"https://example.test/song","license":"https://creativecommons.org/licenses/by/4.0/"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := newFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	var attributed Track
	for _, path := range f.files {
		if path == oneShot {
			attributed = f.track(path)
		}
	}
	if attributed.AttributionURL != "https://example.test/song" || attributed.LicenseURL == "" {
		t.Fatalf("attribution not loaded: %+v", attributed)
	}
	if err := os.Remove(oneShot); err != nil {
		t.Fatal(err)
	}
	f.played = map[string]bool{keep: true}
	done := make(chan error, 1)
	go func() { _, err := f.Next(); done <- err }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Next deadlocked after a one-shot track was removed")
	}
}
