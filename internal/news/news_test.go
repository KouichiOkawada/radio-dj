package news

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReserveMarksSeenOnlyAfterAiring(t *testing.T) {
	now := time.Now()
	feedXML := fmt.Sprintf(`<?xml version="1.0"?><rss><channel>
		<item><title>newest</title><link>https://example.test/1</link><pubDate>%s</pubDate></item>
		<item><title>next</title><link>https://example.test/2</link><pubDate>%s</pubDate></item>
	</channel></rss>`, now.Format(time.RFC1123Z), now.Add(-time.Minute).Format(time.RFC1123Z))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(feedXML))
	}))
	defer server.Close()

	dir := t.TempDir()
	queue := NewQueue(dir)
	feeds := []Feed{{Name: "test", URL: server.URL}}

	first, ok := queue.Reserve(feeds, 6*time.Hour)
	if !ok || first.Title != "newest" {
		t.Fatalf("first reservation = %#v, %v", first, ok)
	}
	second, ok := queue.Reserve(feeds, 6*time.Hour)
	if !ok || second.Title != "next" {
		t.Fatalf("second reservation = %#v, %v", second, ok)
	}
	if _, err := os.Stat(queue.path); !os.IsNotExist(err) {
		t.Fatalf("reservation was persisted as aired: %v", err)
	}

	queue.MarkAired(first)
	data, err := os.ReadFile(queue.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/1") || strings.Contains(string(data), "/2") {
		t.Fatalf("unexpected aired state: %s", data)
	}

	queue.Release(second)
	again, ok := queue.Reserve(feeds, 6*time.Hour)
	if !ok || again.Title != "next" {
		t.Fatalf("released story was not selectable again: %#v, %v", again, ok)
	}
}

func TestFetchKeepsEnoughCandidatesForContinuousNews(t *testing.T) {
	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0"?><rss><channel>`)
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&xml, `<item><title>story %d</title><link>https://example.test/%d</link><pubDate>%s</pubDate></item>`, i, i, time.Now().Add(-time.Duration(i)*time.Minute).Format(time.RFC1123Z))
	}
	xml.WriteString(`</channel></rss>`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(xml.String()))
	}))
	defer server.Close()

	items := Fetch([]Feed{{Name: "busy", URL: server.URL, Category: "stock"}})
	if len(items) != 30 {
		t.Fatalf("Fetch returned %d candidates, want 30", len(items))
	}
}

func TestReserveBulletinBalancesFullNews(t *testing.T) {
	now := time.Now()
	q := NewQueue(t.TempDir())
	items := []Item{
		{Source: "stocks", Category: "stock", Title: "stocks", URL: "https://example.test/stocks", PublishedAt: now.Add(time.Minute).Format(time.RFC1123Z)},
		{Source: "market", Category: "finance", Title: "市場", URL: "https://example.test/market", PublishedAt: now.Format(time.RFC1123Z)},
		{Source: "top", Category: "general", Title: "国内", URL: "https://example.test/top", PublishedAt: now.Add(-time.Minute).Format(time.RFC1123Z)},
		{Source: "sapporo", Category: "hokkaido", Title: "札幌", URL: "https://example.test/sapporo", PublishedAt: now.Add(-2 * time.Minute).Format(time.RFC1123Z)},
	}
	got := q.ReserveBulletin(items, ProgramFull)
	if len(got) != 4 {
		t.Fatalf("got %d items, want 4: %#v", len(got), got)
	}
	seen := map[string]bool{}
	for _, item := range got {
		seen[item.Category] = true
	}
	for _, category := range []string{"stock", "finance", "general", "hokkaido"} {
		if !seen[category] {
			t.Fatalf("missing %s in %#v", category, got)
		}
	}
}

func TestReserveItemsPreferredChoosesStockCategory(t *testing.T) {
	now := time.Now()
	q := NewQueue(t.TempDir())
	items := []Item{
		{Source: "top", Category: "general", Title: "newest general", URL: "https://example.test/general", PublishedAt: now.Format(time.RFC1123Z)},
		{Source: "stocks", Category: "stock", Title: "stock", URL: "https://example.test/stock", PublishedAt: now.Add(-time.Minute).Format(time.RFC1123Z)},
	}
	got, ok := q.ReserveItemsPreferred(items, 6*time.Hour, "stock")
	if !ok || got.Category != "stock" {
		t.Fatalf("stock selection = %#v, %v", got, ok)
	}
}

func TestReserveBulletinFlashRejectsOldItems(t *testing.T) {
	q := NewQueue(t.TempDir())
	got := q.ReserveBulletin([]Item{{Source: "top", Title: "old", URL: "https://example.test/old", PublishedAt: time.Now().Add(-31 * time.Minute).Format(time.RFC1123Z)}}, ProgramFlash)
	if len(got) != 0 {
		t.Fatalf("old item selected for flash: %#v", got)
	}
}

func TestReserveItemsPreferredChoosesFreshPreferredCategory(t *testing.T) {
	now := time.Now()
	q := NewQueue(t.TempDir())
	items := []Item{
		{Source: "top", Category: "general", Title: "newest general", URL: "https://example.test/general", PublishedAt: now.Format(time.RFC1123Z)},
		{Source: "market", Category: "finance", Title: "finance", URL: "https://example.test/finance", PublishedAt: now.Add(-time.Minute).Format(time.RFC1123Z)},
	}
	got, ok := q.ReserveItemsPreferred(items, 6*time.Hour, "finance")
	if !ok || got.Category != "finance" {
		t.Fatalf("preferred selection = %#v, %v", got, ok)
	}
}
