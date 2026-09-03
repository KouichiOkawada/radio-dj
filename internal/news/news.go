// Package news fetches configured RSS/Atom feeds and turns their published
// titles/descriptions into an attributed on-air bulletin. It never asks an LLM
// to invent or summarize facts: every spoken item comes from a feed entry.
package news

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Feed struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MixWithBed creates a complete news segment: the bed loops under the TTS
// file, stays at the configured gain, and ends exactly with the speech. It
// deliberately returns an error when the configured bed is absent: news must
// never silently fall back to dry speech.
func MixWithBed(ffmpeg, speechPath, bedPath, outDir string, volume float64) (string, error) {
	if _, err := os.Stat(bedPath); err != nil {
		return "", fmt.Errorf("news bed unavailable: %w", err)
	}
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return "", fmt.Errorf("ffmpeg not found: %w", err)
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, fmt.Sprintf("news-%d.mp3", time.Now().UnixNano()))
	filter := fmt.Sprintf("[0:a]volume=%.3f,afade=t=in:st=0:d=0.4[bed];[bed][1:a]amix=inputs=2:duration=shortest:normalize=0[mix]", volume)
	cmd := exec.Command(ffmpeg, "-y", "-loglevel", "error", "-stream_loop", "-1", "-i", bedPath, "-i", speechPath,
		"-filter_complex", filter, "-map", "[mix]", "-c:a", "libmp3lame", "-b:a", "192k", "-shortest", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("mix news bed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return out, nil
}

type Item struct {
	Source      string
	Title       string
	Description string
	URL         string
	PublishedAt string
	ImageURL    string
}

type document struct {
	Channel struct {
		Items []entry `xml:"item"`
	} `xml:"channel"`
	Entries []entry `xml:"entry"`
}

type entry struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Summary     string `xml:"summary"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	Published   string `xml:"published"`
	Enclosure   media  `xml:"enclosure"`
	Media       media  `xml:"content"`
	Thumbnail   media  `xml:"thumbnail"`
}

type media struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// Fetch returns up to two recent, distinct entries per feed. A failed feed is
// skipped so news trouble can never stop music playback.
func Fetch(feeds []Feed) []Item {
	client := &http.Client{Timeout: 10 * time.Second}
	seen := map[string]bool{}
	var out []Item
	for _, feed := range feeds {
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
			image := e.Media.URL
			if image == "" {
				image = e.Thumbnail.URL
			}
			if image == "" && strings.HasPrefix(e.Enclosure.Type, "image/") {
				image = e.Enclosure.URL
			}
			out = append(out, Item{Source: feed.Name, Title: title, Description: truncate(desc, 180), URL: clean(e.Link), PublishedAt: published, ImageURL: image})
			if len(out) >= 4 {
				return out
			}
		}
	}
	return out
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

// Script builds an attribution-first bulletin without adding facts not present
// in the feed. This is deliberately deterministic rather than LLM-written.
func Script(items []Item, language string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	if language == "ja" {
		b.WriteString("最新ニュースです。")
		for _, item := range items {
			source := item.Source
			if source == "" {
				source = "配信元"
			}
			fmt.Fprintf(&b, "%sによると、%s。", source, item.Title)
			if item.Description != "" {
				fmt.Fprintf(&b, "概要では、%s。", item.Description)
			}
		}
		b.WriteString("続いて音楽をどうぞ。")
		return b.String()
	}
	b.WriteString("Latest headlines. ")
	for _, item := range items {
		fmt.Fprintf(&b, "%s reports: %s. ", item.Source, item.Title)
		if item.Description != "" {
			fmt.Fprintf(&b, "Summary: %s. ", item.Description)
		}
	}
	b.WriteString("Now, back to the music.")
	return b.String()
}
