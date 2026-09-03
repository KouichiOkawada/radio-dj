// Package news fetches configured RSS/Atom feeds and turns their published
// titles/descriptions into an attributed on-air bulletin. It never asks an LLM
// to invent or summarize facts: every spoken item comes from a feed entry.
package news

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Feed struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category,omitempty"`
}

// MixWithBed creates a complete news segment. The bed is enhancement only:
// if it is missing, ffmpeg is unavailable, or mixing fails, the dry speech is
// returned instead. A cosmetic BGM problem must never suppress a bulletin.
func MixWithBed(ffmpeg, speechPath, bedPath, outDir string, volume float64) (string, error) {
	if strings.TrimSpace(speechPath) == "" {
		return "", fmt.Errorf("news speech path is empty")
	}
	if strings.TrimSpace(bedPath) == "" {
		return speechPath, nil
	}
	if _, err := os.Stat(bedPath); err != nil {
		return speechPath, nil
	}
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return speechPath, nil
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return speechPath, nil
	}
	out := filepath.Join(outDir, fmt.Sprintf("news-%d.mp3", time.Now().UnixNano()))
	filter := fmt.Sprintf("[0:a]volume=%.3f,afade=t=in:st=0:d=0.4[bed];[bed][1:a]amix=inputs=2:duration=shortest:normalize=0[mix]", volume)
	cmd := exec.Command(ffmpeg, "-y", "-loglevel", "error", "-stream_loop", "-1", "-i", bedPath, "-i", speechPath,
		"-filter_complex", filter, "-map", "[mix]", "-c:a", "libmp3lame", "-b:a", "192k", "-shortest", out)
	if _, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(out)
		return speechPath, nil
	}
	return out, nil
}

type Item struct {
	Source      string
	Category    string
	Title       string
	Description string
	URL         string
	PublishedAt string
	ImageURL    string
}

// Queue selects current items across every configured feed and remembers when
// each one was queued. The policy prefers genuinely fresh unseen stories, then
// recent replays after a cooldown, and only then older same-day items. This
// keeps a personal radio useful for hours without pretending stale backlog is
// breaking news or going silent once every fresh URL has been heard once.
type Queue struct {
	path     string
	seen     map[string]time.Time
	reserved map[string]time.Time
	mu       sync.Mutex
}

func NewQueue(stateDir string) *Queue {
	q := &Queue{
		path:     filepath.Join(stateDir, "news-seen.json"),
		seen:     map[string]time.Time{},
		reserved: map[string]time.Time{},
	}
	if b, err := os.ReadFile(q.path); err == nil {
		_ = json.Unmarshal(b, &q.seen)
	}
	return q
}

func (q *Queue) Next(feeds []Feed, maxAge time.Duration) (Item, bool) {
	item, ok := q.Reserve(feeds, maxAge)
	if ok {
		q.MarkAired(item)
	}
	return item, ok
}

// Reserve selects a story for background rendering without marking it aired.
// A reservation prevents parallel preload slots from selecting the same item.
// It is intentionally memory-only: after a restart, a prepared-but-never-aired
// story remains eligible instead of being lost from the station permanently.
func (q *Queue) Reserve(feeds []Feed, maxAge time.Duration) (Item, bool) {
	// Network access must stay outside the queue lock. MarkAired and Release are
	// called by the playback loop and must never wait behind slow RSS servers.
	return q.ReserveItems(Fetch(feeds), maxAge)
}

// ReserveItems selects from an already-collected snapshot. It is used by the
// background news engine so programme preparation never waits for RSS.
func (q *Queue) ReserveItems(items []Item, maxAge time.Duration) (Item, bool) {
	if len(items) == 0 {
		return Item{}, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.pruneReservations()

	// Six hours is the preferred "current" window. Old configs used 72h;
	// honoring that literally made the station call yesterday's stories new.
	preferredAge := maxAge
	if preferredAge <= 0 || preferredAge > 6*time.Hour {
		preferredAge = 6 * time.Hour
	}

	// 1) Fresh unseen stories first.
	if item, ok := q.pickUnseen(items, preferredAge); ok {
		q.reserve(item)
		return item, true
	}

	// 2) A current story may be repeated on a long-running radio, but not every
	// cycle. This is preferable to draining an old backlog just to avoid silence.
	if item, ok := q.pickReplay(items, preferredAge, 90*time.Minute); ok {
		q.reserve(item)
		return item, true
	}

	// 3) If feeds are quiet, allow an unseen item from the last 24 hours. The
	// spoken script includes its actual publication time and does not say it is
	// the latest headline.
	if item, ok := q.pickUnseen(items, 24*time.Hour); ok {
		q.reserve(item)
		return item, true
	}

	// 4) Final radio-friendly fallback: replay a <=24h story only after a long
	// cooldown. If even that is unavailable, caller should simply keep music on.
	if item, ok := q.pickReplay(items, 24*time.Hour, 3*time.Hour); ok {
		q.reserve(item)
		return item, true
	}
	return Item{}, false
}

// ReserveItemsPreferred gives the listener's current topic preference first
// refusal while preserving all freshness, replay-cooldown and reservation
// rules. If that category has no usable story, normal selection continues.
func (q *Queue) ReserveItemsPreferred(items []Item, maxAge time.Duration, preferred string) (Item, bool) {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred == "" {
		return q.ReserveItems(items, maxAge)
	}
	ordered := make([]Item, 0, len(items))
	for _, item := range items {
		if strings.EqualFold(item.Category, preferred) {
			ordered = append(ordered, item)
		}
	}
	for _, item := range items {
		if !strings.EqualFold(item.Category, preferred) {
			ordered = append(ordered, item)
		}
	}
	return q.ReserveItems(ordered, maxAge)
}

// ReserveBulletin selects a balanced, deterministic set from a collector
// snapshot. FULL NEWS prefers finance, general and Hokkaido in that order;
// FLASH is intentionally short and only accepts stories from the last 30
// minutes. Reservation is still in-memory until audio actually airs.
func (q *Queue) ReserveBulletin(items []Item, kind ProgramKind) []Item {
	if len(items) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pruneReservations()

	limit := 3
	maxAge := 6 * time.Hour
	if kind == ProgramFlash {
		limit, maxAge = 2, 30*time.Minute
	}
	usedSource := map[string]int{}
	usedTitle := map[string]bool{}
	selected := make([]Item, 0, limit)
	pick := func(category string, age time.Duration) {
		if len(selected) >= limit {
			return
		}
		for _, item := range items {
			itemCategory := strings.ToLower(strings.TrimSpace(item.Category))
			if itemCategory == "" {
				itemCategory = "general"
			}
			if category != "" && itemCategory != category {
				continue
			}
			if !fresh(item.PublishedAt, age) || usedSource[item.Source] >= 2 {
				continue
			}
			key := itemKey(item)
			titleKey := normalizeTitle(item.Title)
			if _, aired := q.seen[key]; aired || q.reserved[key].IsZero() == false || usedTitle[titleKey] {
				continue
			}
			q.reserve(item)
			usedSource[item.Source]++
			usedTitle[titleKey] = true
			selected = append(selected, item)
			return
		}
	}
	if kind == ProgramFull || kind == ProgramMorningMarket || kind == ProgramTokyoClose {
		pick("finance", 4*time.Hour)
		pick("general", 6*time.Hour)
		pick("hokkaido", 12*time.Hour)
	} else {
		pick("", maxAge)
		pick("", maxAge)
	}
	// Never pad with old news just to hit a quota. A fresh diverse story is a
	// valid replacement when a category has no candidate.
	for len(selected) < limit {
		before := len(selected)
		pick("", maxAge)
		if len(selected) == before {
			break
		}
	}
	return selected
}

var titleNoise = regexp.MustCompile(`[\s\p{P}\p{S}]+`)

func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return titleNoise.ReplaceAllString(s, "")
}

func itemKey(item Item) string {
	if strings.TrimSpace(item.URL) != "" {
		return strings.TrimSpace(item.URL)
	}
	return item.Source + "\n" + item.Title
}

func (q *Queue) pickUnseen(items []Item, maxAge time.Duration) (Item, bool) {
	for _, item := range items {
		if !fresh(item.PublishedAt, maxAge) {
			continue
		}
		key := itemKey(item)
		if _, played := q.seen[key]; played {
			continue
		}
		if _, pending := q.reserved[key]; pending {
			continue
		}
		return item, true
	}
	return Item{}, false
}

func (q *Queue) pickReplay(items []Item, maxAge, cooldown time.Duration) (Item, bool) {
	now := time.Now()
	for _, item := range items {
		if !fresh(item.PublishedAt, maxAge) {
			continue
		}
		key := itemKey(item)
		if _, pending := q.reserved[key]; pending {
			continue
		}
		last, played := q.seen[key]
		if !played || now.Sub(last) < cooldown {
			continue
		}
		return item, true
	}
	return Item{}, false
}

func (q *Queue) reserve(item Item) {
	q.reserved[itemKey(item)] = time.Now()
}

// MarkAired commits a reserved story to persistent history when playback
// actually reaches the news segment.
func (q *Queue) MarkAired(item Item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.reserved, itemKey(item))
	q.remember(item)
}

// Release makes a prepared story selectable again when rendering fails or a
// prefetched batch is discarded by a mode change.
func (q *Queue) Release(item Item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.reserved, itemKey(item))
}

func (q *Queue) pruneReservations() {
	cutoff := time.Now().Add(-3 * time.Hour)
	for key, reservedAt := range q.reserved {
		if reservedAt.Before(cutoff) {
			delete(q.reserved, key)
		}
	}
}

func (q *Queue) remember(item Item) {
	q.seen[itemKey(item)] = time.Now()
	q.prune()
	q.save()
}

var publishedLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC850,
	time.ANSIC,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
}

func parsePublished(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range publishedLayouts {
		if published, err := time.Parse(layout, value); err == nil {
			return published, true
		}
	}
	return time.Time{}, false
}

func fresh(value string, maxAge time.Duration) bool {
	if maxAge <= 0 {
		return true
	}
	published, ok := parsePublished(value)
	if !ok {
		return false // without a publication date it cannot be called current news
	}
	age := time.Since(published)
	return age >= -15*time.Minute && age <= maxAge
}

func (q *Queue) prune() {
	if len(q.seen) <= 1000 {
		return
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for key, aired := range q.seen {
		if aired.Before(cutoff) {
			delete(q.seen, key)
		}
	}
}

func (q *Queue) save() {
	b, err := json.MarshalIndent(q.seen, "", "  ")
	if err == nil {
		_ = os.WriteFile(q.path, b, 0o644)
	}
}

type document struct {
	Channel struct {
		Items []entry `xml:"item"`
	} `xml:"channel"`
	Entries []entry `xml:"entry"`
}

type entry struct {
	Title       string   `xml:"title"`
	Description string   `xml:"description"`
	Summary     string   `xml:"summary"`
	Link        feedLink `xml:"link"`
	PubDate     string   `xml:"pubDate"`
	Published   string   `xml:"published"`
	Updated     string   `xml:"updated"`
	Enclosure   media    `xml:"enclosure"`
	Media       media    `xml:"content"`
	Thumbnail   media    `xml:"thumbnail"`
}

type feedLink struct {
	Href string `xml:"href,attr"`
	Text string `xml:",chardata"`
}

type media struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// Fetch returns distinct entries across feeds, sorted newest-first by their
// actual publication timestamp. A failed feed is skipped so news trouble can
// never stop music playback.
func Fetch(feeds []Feed) []Item {
	client := &http.Client{Timeout: 10 * time.Second}
	seen := map[string]bool{}
	var out []Item
	for _, feed := range feeds {
		added := 0
		if strings.TrimSpace(feed.URL) == "" {
			continue
		}
		resp, err := client.Get(feed.URL)
		if err != nil || resp.StatusCode >= 300 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if err != nil {
			continue
		}
		var doc document
		if xml.Unmarshal(body, &doc) != nil {
			continue
		}
		entries := doc.Channel.Items
		if len(entries) == 0 {
			entries = doc.Entries
		}
		for _, e := range entries {
			title := clean(e.Title)
			if title == "" || seen[title] {
				continue
			}
			seen[title] = true
			desc := clean(e.Description)
			if desc == "" {
				desc = clean(e.Summary)
			}
			published := clean(e.PubDate)
			if published == "" {
				published = clean(e.Published)
			}
			if published == "" {
				published = clean(e.Updated)
			}
			image := e.Media.URL
			if image == "" {
				image = e.Thumbnail.URL
			}
			if image == "" && strings.HasPrefix(e.Enclosure.Type, "image/") {
				image = e.Enclosure.URL
			}
			url := clean(e.Link.Text)
			if url == "" {
				url = clean(e.Link.Href)
			}
			out = append(out, Item{Source: feed.Name, Category: feed.Category, Title: title, Description: truncate(desc, 180), URL: url, PublishedAt: published, ImageURL: image})
			added++
			// Keep one high-volume feed from consuming unbounded work while still
			// taking enough candidates to find the globally freshest entry.
			if added >= 20 {
				break
			}
		}
	}

	sortItemsNewest(out)
	return out
}

func sortItemsNewest(out []Item) {
	sort.SliceStable(out, func(i, j int) bool {
		a, aok := parsePublished(out[i].PublishedAt)
		b, bok := parsePublished(out[j].PublishedAt)
		switch {
		case aok && bok:
			return a.After(b)
		case aok:
			return true
		case bok:
			return false
		default:
			return false
		}
	})
}

var imageCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

var ogImage = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)=["']og:image["'][^>]+content=["']([^"']+)["']`)
var ogImageReverse = regexp.MustCompile(`(?is)<meta[^>]+content=["']([^"']+)["'][^>]+(?:property|name)=["']og:image["']`)

// ResolveImage prefers RSS media fields, then makes one bounded OGP request
// for the article. Failures leave ImageURL empty; no placeholder is invented.
func ResolveImage(item *Item) {
	if item == nil || item.ImageURL != "" || !strings.HasPrefix(item.URL, "http") {
		return
	}
	imageCache.Lock()
	if cached, ok := imageCache.m[item.URL]; ok {
		item.ImageURL = cached
		imageCache.Unlock()
		return
	}
	imageCache.Unlock()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(item.URL)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	match := ogImage.FindSubmatch(body)
	if len(match) < 2 {
		match = ogImageReverse.FindSubmatch(body)
	}
	if len(match) < 2 {
		return
	}
	item.ImageURL = html.UnescapeString(string(match[1]))
	imageCache.Lock()
	imageCache.m[item.URL] = item.ImageURL
	imageCache.Unlock()
}

var tags = regexp.MustCompile(`<[^>]*>`)

func clean(s string) string {
	s = html.UnescapeString(tags.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func spokenPublished(value, language string) string {
	published, ok := parsePublished(value)
	if !ok {
		return ""
	}
	local := published.Local()
	now := time.Now()
	if language == "ja" {
		y1, m1, d1 := now.Date()
		y2, m2, d2 := local.Date()
		if y1 == y2 && m1 == m2 && d1 == d2 {
			return fmt.Sprintf("今日%d時%02d分配信", local.Hour(), local.Minute())
		}
		return fmt.Sprintf("%d月%d日%d時%02d分配信", int(m2), d2, local.Hour(), local.Minute())
	}
	return local.Format("Jan 2 15:04")
}

func japaneseLead(items []Item) string {
	if len(items) == 0 {
		return ""
	}
	published, ok := parsePublished(items[0].PublishedAt)
	if !ok {
		return "ニュースをお伝えします。"
	}
	age := time.Since(published)
	if age >= -15*time.Minute && age <= 3*time.Hour {
		return "ただいま入っているニュースをお伝えします。"
	}
	local := published.Local()
	now := time.Now()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := local.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return "今日のニュースからお伝えします。"
	}
	return "直近のニュースからお伝えします。"
}

// Script builds an attribution-first bulletin without adding facts not present
// in the feed. This is deliberately deterministic rather than LLM-written.
func Script(items []Item, language string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	if language == "ja" {
		b.WriteString(japaneseLead(items))
		for _, item := range items {
			source := item.Source
			if source == "" {
				source = "配信元"
			}
			if when := spokenPublished(item.PublishedAt, language); when != "" {
				fmt.Fprintf(&b, "%s、%sによると、%s。", when, source, item.Title)
			} else {
				fmt.Fprintf(&b, "%sによると、%s。", source, item.Title)
			}
			if item.Description != "" {
				fmt.Fprintf(&b, "概要では、%s。", item.Description)
			}
		}
		return b.String()
	}
	b.WriteString("Here are the current headlines. ")
	for _, item := range items {
		fmt.Fprintf(&b, "%s reports: %s. ", item.Source, item.Title)
		if item.Description != "" {
			fmt.Fprintf(&b, "Summary: %s. ", item.Description)
		}
	}
	return b.String()
}
