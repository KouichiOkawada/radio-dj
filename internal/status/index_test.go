package status

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// End-to-end render of the player index — the path POST /control's UI travels.
// Drives the full chain: i18n load → struct population → template execute.
// A missing struct field, a broken i18n JSON-island pipeline, or a dropped
// {{.Previous}}/{{.Skip}} binding would all surface only on first page load
// without this check.
func TestServeIndexRendersSkipControls(t *testing.T) {
	s := &Server{lang: "en", mount: "/stream.aac"}
	rec := httptest.NewRecorder()
	s.serveIndex(rec)

	body := rec.Body.String()
	if strings.Contains(body, "{{.") {
		i := strings.Index(body, "{{.")
		t.Fatalf("unrendered template action left in page: %q", body[i:i+12])
	}
	for _, want := range []string{
		"PREV ◀",             // ui_previous rendered on the ◀◀ button
		"SKIP ▶",             // ui_skip rendered on the ▶▶ button
		"control('previous'", // ◀◀ button → previous action
		"control('next'",     // ▶▶ button → next action
		"/control",           // the fetch target
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index render missing %q", want)
		}
	}
}

func TestPlayerShellUsesLocalAvatarAndFreshHTML(t *testing.T) {
	s := &Server{lang: "ja", mount: "/stream.mp3"}
	rec := httptest.NewRecorder()
	s.serveIndex(rec)
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control=%q, want no-cache", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `src="/dj-avatar.svg"`) {
		t.Error("local DJ avatar is missing")
	}
	if strings.Contains(body, "api.dicebear.com") || strings.Contains(body, "news-readiness.js") {
		t.Error("player still depends on a removed runtime UI asset")
	}
}
