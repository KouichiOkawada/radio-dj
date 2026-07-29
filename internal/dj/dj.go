// Package dj generates on-air speech via an OpenAI-compatible endpoint
// (GLM-4.6 on Z.ai by default). GLM is a thinking model — we disable thinking
// so the answer lands in `content` instead of burning the budget in reasoning.
// Emojis are stripped in code (the model ignores "no emojis" in the prompt).
//
// All copy (system persona, banter/weather/request phrasing, weather words)
// lives in internal/i18n/prompts/{lang}.json — this package only composes it.
package dj

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"radio-dj/internal/i18n"
)

type DJ struct {
	baseURL, apiKey, model string
	station, location      string
	p                      i18n.Prompts
}

// New builds a DJ. p is the localized prompt set (see internal/i18n).
func New(baseURL, apiKey, model, station, location string, p i18n.Prompts) *DJ {
	return &DJ{baseURL: baseURL, apiKey: apiKey, model: model, station: station, location: location, p: p}
}

// systemPrompt fills the standing persona with station + location.
func (d *DJ) systemPrompt() string {
	loc := d.location
	if loc == "" {
		loc = "Bolivia"
	}
	return d.p.Sub("system", map[string]string{"station": d.station, "location": loc})
}

// credit builds the " by {artist} (from {album})" tail shared by banter +
// request acks. Empty when neither is present.
func (d *DJ) credit(artist, album string) string {
	switch {
	case artist != "" && album != "":
		return d.p.Sub("credit_artist_album", map[string]string{"artist": artist, "album": album})
	case artist != "":
		return d.p.Sub("credit_artist", map[string]string{"artist": artist})
	}
	return ""
}

// Say runs a single chat completion (no web search) and returns clean text.
func (d *DJ) Say(user string) string {
	return d.complete(user, false)
}

// SaySearch runs a completion WITH web search enabled (GLM's web_search tool),
// for live facts. Used by SayWiki.
func (d *DJ) SaySearch(user string) string {
	return d.complete(user, true)
}

// Banter is a between-track intro for a song.
func (d *DJ) Banter(title, artist, album string) string {
	return d.Say(d.p.Sub("banter", map[string]string{
		"title":  title,
		"credit": d.credit(artist, album),
	}))
}

// SayRequest phrases a listener's request acknowledgment.
func (d *DJ) SayRequest(title, artist, req string) string {
	return d.Say(d.p.Sub("request_ack", map[string]string{
		"req":    req,
		"title":  title,
		"credit": d.credit(artist, ""),
	}))
}

// SayWiki asks for a real, web-searched fact about the artist/title.
func (d *DJ) SayWiki(artist, title string) string {
	return d.SaySearch(d.p.Sub("wiki", map[string]string{"subject": d.wikiSubject(artist, title)}))
}

// wikiSubject builds the "el artista X o la canción Y" phrase for the wiki ask.
func (d *DJ) wikiSubject(artist, title string) string {
	switch {
	case artist != "" && title != "":
		return d.p.Sub("wiki_artist_or_title", map[string]string{"artist": artist, "title": title})
	case artist != "":
		return d.p.Sub("wiki_artist", map[string]string{"artist": artist})
	case title != "":
		return d.p.Sub("wiki_title", map[string]string{"title": title})
	}
	return d.p.Get("wiki_any")
}

// SayWeather phrases a weather reading. cond is a WMO category (clear/clouds/
// fog/rain/snow/showers/storm/change); ok=false (API failed) → generic fallback.
func (d *DJ) SayWeather(location, cond string, temp int, ok bool) string {
	if !ok {
		return d.Say(d.p.Sub("weather_fallback", map[string]string{"location": location}))
	}
	return d.Say(d.p.Sub("weather_ok", map[string]string{
		"location": location,
		"temp":     fmt.Sprintf("%d", temp),
		"desc":     d.p.Get("wmo_" + cond),
	}))
}

// complete is the shared chat call. search=true enables web_search (GLM).
func (d *DJ) complete(user string, search bool) string {
	body := map[string]any{
		"model": d.model,
		"messages": []map[string]string{
			{"role": "system", "content": d.systemPrompt()},
			{"role": "user", "content": user},
		},
		"max_tokens": 160,
		"thinking":   map[string]string{"type": "disabled"},
	}
	if search {
		body["web_search"] = true
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", strings.TrimRight(d.baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	r, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return ""
	}
	var doc struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(r, &doc) != nil || len(doc.Choices) == 0 {
		return ""
	}
	return stripEmoji(strings.TrimSpace(doc.Choices[0].Message.Content))
}

// stripEmoji removes emoji + misc symbols the TTS would read aloud or garble.
var emojiRx = regexp.MustCompile(`[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}\x{2190}-\x{21FF}\x{2B00}-\x{2BFF}]`)

func stripEmoji(s string) string {
	return emojiRx.ReplaceAllString(s, "")
}
