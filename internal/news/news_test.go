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
