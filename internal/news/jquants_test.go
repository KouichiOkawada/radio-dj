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
		case "/v2/equities/bars/daily":
			if r.URL.Query().Get("from") == "" || r.URL.Query().Get("to") == "" {
				t.Fatal("daily bars request is missing date range")
			}
			_, _ = w.Write([]byte(`{"data":[{"Date":"2026-09-02","C":510},{"Date":"2026-09-03","C":525}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	j := NewJQuants("secret")
	j.baseURL = server.URL
	got := j.MarketContext(context.Background(), []string{"6768"})
	for _, want := range []string{"テスト社", "最新開示日=2026-08-01", "Sales=20", "OP=3", "2026-09-03 終値=525"} {
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

func TestResolveArticleSymbolsUsesOfficialCompanyNameOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/equities/master" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"Code":"72030","CoName":"トヨタ自動車株式会社"},{"Code":"67680","CoName":"タムラ製作所株式会社"}]}`))
	}))
	defer server.Close()

	j := NewJQuants("test-key")
	j.baseURL = server.URL
	got := j.ResolveArticleSymbols(context.Background(), "トヨタ自動車が新サービスを発表", nil)
	if len(got) != 1 || got[0] != "72030" {
		t.Fatalf("ResolveArticleSymbols() = %v, want [72030]", got)
	}
	got = j.ResolveArticleSymbols(context.Background(), "製作所の景況感について", nil)
	if len(got) != 0 {
		t.Fatalf("ambiguous partial company name must not resolve: %v", got)
	}
}
