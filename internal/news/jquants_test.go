package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJQuantsMarketContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			t.Fatal("missing API key header")
		}
		switch r.URL.Path {
		case "/v2/equities/master":
			_, _ = w.Write([]byte(`{"data":[{"Code":"6768","CoName":"テスト社","MktNm":"プライム","S33Nm":"電気機器"}]}`))
		case "/v2/fins/summary":
			_, _ = w.Write([]byte(`{"data":[{"DiscDate":"2026-01-01","Sales":"10"},{"DiscDate":"2026-08-01","Sales":"20","OP":"3"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	j := NewJQuants("secret")
	j.baseURL = server.URL
	got := j.MarketContext(context.Background(), []string{"6768"})
	for _, want := range []string{"テスト社", "最新開示日=2026-08-01", "Sales=20", "OP=3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context %q does not contain %q", got, want)
		}
	}
}

func TestJQuantsDisabledWithoutKey(t *testing.T) {
	if got := NewJQuants("").MarketContext(context.Background(), []string{"6768"}); got != "" {
		t.Fatalf("expected empty context, got %q", got)
	}
}
