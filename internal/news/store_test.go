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
