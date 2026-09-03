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
	"regexp"
	"strings"
	"time"
)

type Feed struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Item struct {
	Source      string
	Title       string
	Description string
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
			out = append(out, Item{Source: feed.Name, Title: title, Description: truncate(desc, 180)})
			if len(out) >= 4 {
				return out
			}
		}
	}
	return out
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
