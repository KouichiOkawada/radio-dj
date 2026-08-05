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
	"time"

	"radio-dj/internal/i18n"
)

type DJ struct {
	provider               string
	baseURL, apiKey, model string
	station, location      string
	p                      i18n.Prompts
}

// Director plan types — the structured in/out of the one-call-per-tanda DJ
// planner (DirectPlan). The LLM receives a Ctx and returns a Plan.
type (
	// Cand is one candidate/history track handed to the director.
	Cand struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Album  string `json:"album,omitempty"`
	}
	// Req is a listener request (with its match, if any) for the director.
	Req struct {
		From   string `json:"from,omitempty"`
		Query  string `json:"query"`
		Title  string `json:"title,omitempty"`
		Artist string `json:"artist,omitempty"`
	}
	// Ctx is the full structured context the director reasons over.
	Ctx struct {
		Talk       string `json:"talk"`
		TimeOfDay  string `json:"time_of_day,omitempty"`
		History    []Cand `json:"history"`
		Candidates []Cand `json:"candidates"`
		Requests   []Req  `json:"requests,omitempty"`
	}
	// Break is the talk the director schedules for a setlist position.
	Break struct {
		Before int    `json:"before"`           // index into setlist
		Kind   string `json:"kind"`             // intro|trivia|wiki|history|station|time|none
		At     string `json:"at,omitempty"`     // "before" (default) | "mid"
	}
	// Plan is the director's output: an ordered setlist + talk breaks.
	Plan struct {
		Setlist []int   `json:"setlist"`
		Breaks  []Break `json:"breaks"`
	}
)

// New builds a DJ. provider is the LLM preset (glm/openai/openrouter/...) —
// only "glm" accepts the thinking-disabled field; others reject it. p is the
// localized prompt set (see internal/i18n).
func New(provider, baseURL, apiKey, model, station, location string, p i18n.Prompts) *DJ {
	return &DJ{provider: provider, baseURL: baseURL, apiKey: apiKey, model: model, station: station, location: location, p: p}
}

// systemPrompt fills the standing persona with station + location.
// todLabel returns the localized time-of-day word for the hour, looked up in
// the localized prompt set so the model anchors tone to the real local clock
// (GLM defaults to a CN timezone — the explicit {time}/{time_of_day} override
// stops it from calling the afternoon "night").
func (d *DJ) todLabel(t time.Time) string {
	var key string
	switch h := t.Hour(); {
	case h < 6:
		key = "dawn"
	case h < 12:
		key = "morning"
	case h < 19:
		key = "afternoon"
	default:
		key = "night"
	}
	return d.p.Get("tod_" + key)
}

func (d *DJ) systemPrompt() string {
	loc := d.location
	if loc == "" {
		loc = "Bolivia"
	}
	now := time.Now()
	return d.p.Sub("system", map[string]string{
		"station":     d.station,
		"location":    loc,
		"time":        now.Format("15:04"),
		"time_of_day": d.todLabel(now),
	})
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

// Banter is a between-track intro for a song (uses the "intro" prompt).
func (d *DJ) Banter(title, artist, album string) string {
	return d.Say(d.p.Sub("intro", map[string]string{
		"title":  title,
		"credit": d.credit(artist, album),
	}))
}

// SayMidroll is a short mid-song commentary.
func (d *DJ) SayMidroll(title, artist string) string {
	return d.Say(d.p.Sub("midroll", map[string]string{
		"title":  title,
		"credit": d.credit(artist, ""),
	}))
}

// SayRequest phrases a listener's request acknowledgment.
func (d *DJ) SayRequest(title, artist, from, req string) string {
	dedic := " del oyente"
	if from != "" {
		dedic = " de " + from
	}
	return d.Say(d.p.Sub("request_ack", map[string]string{
		"req":    req,
		"title":  title,
		"credit": d.credit(artist, ""),
		"from":   from,
		"dedic":  dedic,
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
	}
	if d.provider == "glm" {
		body["thinking"] = map[string]string{"type": "disabled"}
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

// completeJSON is complete() with response_format=json_object, used by the
// director planner (the plan is longer than a spoken one-liner). Returns the
// raw model content; the caller parses the JSON.
func (d *DJ) completeJSON(system, user string) string {
	body := map[string]any{
		"model": d.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens":      1200,
		"response_format": map[string]string{"type": "json_object"},
	}
	if d.provider == "glm" {
		body["thinking"] = map[string]string{"type": "disabled"}
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
	return strings.TrimSpace(doc.Choices[0].Message.Content)
}

// DirectPlan asks the LLM to plan one tanda: pick+order a coherent setlist
// from candidates and decide where (and whether) to talk, modulated by the
// talkiness dial in ctx.Talk. Returns a validated Plan; on any error the
// caller falls back to random selection so the station never stops.
func (d *DJ) DirectPlan(ctx Ctx) (Plan, error) {
	loc := d.location
	if loc == "" {
		loc = "Bolivia"
	}
	system := d.p.Sub("director", map[string]string{"station": d.station, "location": loc, "talk": ctx.Talk})
	user, _ := json.Marshal(ctx)
	raw := d.completeJSON(system, string(user))
	if raw == "" {
		return Plan{}, fmt.Errorf("respuesta vacía del modelo")
	}
	raw = extractJSON(raw)
	var plan Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return Plan{}, fmt.Errorf("parse del plan: %w", err)
	}
	if len(plan.Setlist) == 0 || len(ctx.Candidates) == 0 {
		return Plan{}, fmt.Errorf("setlist vacío")
	}
	for _, id := range plan.Setlist {
		if id < 0 || id >= len(ctx.Candidates) {
			return Plan{}, fmt.Errorf("setlist id %d fuera de rango (0..%d)", id, len(ctx.Candidates)-1)
		}
	}
	return plan, nil
}

// extractJSON returns the first balanced {...} block in s — a safety net for
// models that wrap JSON in prose or code fences despite json_object mode.
func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return s
	}
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[i : j+1]
			}
		}
	}
	return s[i:]
}

// stripEmoji removes emoji + misc symbols the TTS would read aloud or garble.
var emojiRx = regexp.MustCompile(`[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}\x{2190}-\x{21FF}\x{2B00}-\x{2BFF}]`)

func stripEmoji(s string) string {
	return emojiRx.ReplaceAllString(s, "")
}
