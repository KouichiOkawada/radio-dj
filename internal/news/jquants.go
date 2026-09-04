package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// JQuants adds dated, verified company context to stock-news commentary. It is
// deliberately fail-soft: RSS news still airs if the service is unavailable.
type JQuants struct {
	apiKey           string
	baseURL          string
	client           *http.Client
	mu               sync.Mutex
	cache            map[string]jquantsCache
	masters          []companyRef
	mastersExpiresAt time.Time
}

type companyRef struct {
	code string
	name string
}

type jquantsCache struct {
	text      string
	expiresAt time.Time
}

type jquantsResponse struct {
	Data []map[string]any `json:"data"`
}

func NewJQuants(apiKey string) *JQuants {
	return &JQuants{
		apiKey: strings.TrimSpace(apiKey), baseURL: "https://api.jquants.com",
		client: &http.Client{Timeout: 8 * time.Second}, cache: map[string]jquantsCache{},
	}
}

// MarketContext returns a compact prompt fragment for up to three configured
// watch symbols. Official field names are retained to avoid inventing units.
func (j *JQuants) MarketContext(ctx context.Context, symbols []string) string {
	if j == nil || j.apiKey == "" {
		return ""
	}
	clean := normalizeSymbols(symbols)
	if len(clean) == 0 {
		return ""
	}
	key := strings.Join(clean, ",")
	j.mu.Lock()
	if cached, ok := j.cache[key]; ok && time.Now().Before(cached.expiresAt) {
		j.mu.Unlock()
		return cached.text
	}
	j.mu.Unlock()

	parts := make([]string, 0, len(clean))
	for _, symbol := range clean {
		master, err := j.get(ctx, "/v2/equities/master", symbol)
		if err != nil || len(master) == 0 {
			continue
		}
		line := fmt.Sprintf("銘柄コード %s", symbol)
		if name := stringField(master[0], "CoName"); name != "" {
			line += " / " + name
		}
		if market := stringField(master[0], "MktNm"); market != "" {
			line += " / 市場=" + market
		}
		if sector := stringField(master[0], "S33Nm"); sector != "" {
			line += " / 業種=" + sector
		}
		yesterday := time.Now().AddDate(0, 0, -1)
		bars, berr := j.getRange(ctx, "/v2/equities/bars/daily", symbol, yesterday.AddDate(0, 0, -14), yesterday)
		if berr == nil && len(bars) > 0 {
			sort.SliceStable(bars, func(a, b int) bool { return stringField(bars[a], "Date") > stringField(bars[b], "Date") })
			limit := len(bars)
			if limit > 5 {
				limit = 5
			}
			line += " / 直近株価（新しい順）="
			for i := 0; i < limit; i++ {
				closeValue := firstStringField(bars[i], "C", "Close", "AdjC", "AdjustmentClose")
				if i > 0 {
					line += ", "
				}
				line += stringField(bars[i], "Date") + " 終値=" + closeValue
			}
		}
		summary, serr := j.get(ctx, "/v2/fins/summary", symbol)
		if serr == nil && len(summary) > 0 {
			sort.SliceStable(summary, func(a, b int) bool {
				return stringField(summary[a], "DiscDate")+stringField(summary[a], "DiscTime") >
					stringField(summary[b], "DiscDate")+stringField(summary[b], "DiscTime")
			})
			latest := summary[0]
			if date := stringField(latest, "DiscDate"); date != "" {
				line += " / 最新開示日=" + date
			}
			for _, field := range []string{"DocType", "CurPerType", "Sales", "OP", "NP", "FSales", "FOP", "FNP"} {
				if value := stringField(latest, field); value != "" {
					line += " / " + field + "=" + value
				}
			}
		}
		parts = append(parts, line)
	}
	text := strings.Join(parts, "\n")
	j.mu.Lock()
	j.cache[key] = jquantsCache{text: text, expiresAt: time.Now().Add(30 * time.Minute)}
	j.mu.Unlock()
	return text
}

// ResolveArticleSymbols augments explicitly printed security codes with codes
// whose official company name actually occurs in the article. It deliberately
// avoids fuzzy aliases: a false company/price association is worse than
// omitting optional market context.
func (j *JQuants) ResolveArticleSymbols(ctx context.Context, article string, explicit []string) []string {
	resolved := normalizeSymbols(explicit)
	if j == nil || j.apiKey == "" || len(resolved) >= 3 {
		return resolved
	}
	masters, err := j.companyMaster(ctx)
	if err != nil {
		return resolved
	}
	normalizedArticle := normalizeCompanyText(article)
	for _, company := range masters {
		name := normalizeCompanyText(company.name)
		if len([]rune(name)) < 3 || !strings.Contains(normalizedArticle, name) {
			continue
		}
		resolved = normalizeSymbols(append(resolved, company.code))
		if len(resolved) == 3 {
			break
		}
	}
	return resolved
}

func (j *JQuants) companyMaster(ctx context.Context) ([]companyRef, error) {
	j.mu.Lock()
	if len(j.masters) > 0 && time.Now().Before(j.mastersExpiresAt) {
		out := append([]companyRef(nil), j.masters...)
		j.mu.Unlock()
		return out, nil
	}
	j.mu.Unlock()
	u, err := url.Parse(strings.TrimRight(j.baseURL, "/") + "/v2/equities/master")
	if err != nil {
		return nil, err
	}
	records, err := j.getURL(ctx, u)
	if err != nil {
		return nil, err
	}
	masters := make([]companyRef, 0, len(records))
	for _, record := range records {
		code, name := stringField(record, "Code"), stringField(record, "CoName")
		if code != "" && name != "" {
			masters = append(masters, companyRef{code: code, name: name})
		}
	}
	j.mu.Lock()
	j.masters = append([]companyRef(nil), masters...)
	j.mastersExpiresAt = time.Now().Add(24 * time.Hour)
	j.mu.Unlock()
	return masters, nil
}

func normalizeCompanyText(value string) string {
	replacer := strings.NewReplacer(" ", "", "　", "", "株式会社", "", "（株）", "", "(株)", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(value)))
}

func (j *JQuants) getRange(ctx context.Context, path, symbol string, from, to time.Time) ([]map[string]any, error) {
	u, err := url.Parse(strings.TrimRight(j.baseURL, "/") + path)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("code", symbol)
	q.Set("from", from.Format("20060102"))
	q.Set("to", to.Format("20060102"))
	u.RawQuery = q.Encode()
	return j.getURL(ctx, u)
}

func normalizeSymbols(symbols []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 3)
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" || seen[symbol] {
			continue
		}
		seen[symbol] = true
		out = append(out, symbol)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func (j *JQuants) get(ctx context.Context, path, symbol string) ([]map[string]any, error) {
	u, err := url.Parse(strings.TrimRight(j.baseURL, "/") + path)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("code", symbol)
	u.RawQuery = q.Encode()
	return j.getURL(ctx, u)
}

func (j *JQuants) getURL(ctx context.Context, u *url.URL) ([]map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", j.apiKey)
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("J-Quants %s returned HTTP %d", u.Path, resp.StatusCode)
	}
	var payload jquantsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

func firstStringField(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringField(record, key); value != "" {
			return value
		}
	}
	return ""
}

func stringField(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.6f", typed), "0"), ".")
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
