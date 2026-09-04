package news

import (
	"testing"
	"time"
)

func TestStoreSnapshotIsNewestFirst(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Update([]Item{
		{URL: "https://example.test/old", Title: "old", PublishedAt: now.Add(-time.Hour).Format(time.RFC1123Z)},
		{URL: "https://example.test/new", Title: "new", PublishedAt: now.Format(time.RFC1123Z)},
	})
	got := s.Snapshot()
	if len(got) != 2 || got[0].Title != "new" {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestStoreExcludesConfiguredTopics(t *testing.T) {
	s := NewStore("タムラ製作所")
	s.Update([]Item{
		{Title: "タムラ製作所が決算を発表", URL: "https://example.test/tamura"},
		{Title: "別の上場企業が決算を発表", URL: "https://example.test/other"},
	})
	got := s.Snapshot()
	if len(got) != 1 || got[0].Title != "別の上場企業が決算を発表" {
		t.Fatalf("excluded topic leaked into store: %#v", got)
	}
}
