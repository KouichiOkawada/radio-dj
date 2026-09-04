package dj

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"radio-dj/internal/i18n"
)

func TestNewsBulletinSendsRSSFactsToAI(t *testing.T) {
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		prompt = body.Messages[len(body.Messages)-1].Content
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"AIで要約した放送原稿です。"}}]}`))
	}))
	defer server.Close()

	host := New("openai", server.URL, "test-key", "test-model", "test", "Tokyo", i18n.Prompts{})
	got := host.NewsBulletin("テスト通信", "企業が新製品を発表", "概要の事実です", "2026-09-04T10:00:00+09:00")
	if got != "AIで要約した放送原稿です。" {
		t.Fatalf("NewsBulletin()=%q", got)
	}
	for _, want := range []string{"テスト通信", "企業が新製品を発表", "概要の事実です", "RSS配信時刻"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("AI prompt missing %q", want)
		}
	}
}

func TestRequestGuardPreventsHTTPCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"unexpected"}}]}`))
	}))
	defer server.Close()
	host := New("openai", server.URL, "test-key", "test-model", "test", "Tokyo", i18n.Prompts{})
	host.SetRequestAllowed(func() bool { return false })
	if got := host.NewsBulletin("source", "title", "description", "now"); got != "" {
		t.Fatalf("guarded completion=%q, want empty", got)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls=%d, want 0", calls)
	}
}
