// Package dj generates on-air speech via an OpenAI-compatible endpoint
// (GLM-5.2 on Z.ai). GLM is a thinking model — we disable thinking so the
// answer lands in `content` instead of burning the budget in reasoning.
// Emojis are stripped in code (the model ignores "no emojis" in the prompt).
package dj

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type DJ struct {
	baseURL, apiKey, model string
	station, location      string
}

func New(baseURL, apiKey, model, station, location string) *DJ {
	return &DJ{baseURL: baseURL, apiKey: apiKey, model: model, station: station, location: location}
}

// systemPrompt is the DJ's standing persona — ported from subwave's essence:
// station + on-air location + voice rules. No emojis, no quotes, no markdown,
// plain read-aloud Spanish (Colombian tuteo).
func (d *DJ) systemPrompt() string {
	loc := d.location
	if loc == "" {
		loc = "Bolivia"
	}
	return fmt.Sprintf(`Sos el DJ al aire de %s, una radio personal transmitiendo desde %s.
Estilo: cálido, cercano, en español colombiano (tuteo), con humor seco. Sos un
conductor real, no un asistente: presentás temas, tirás datos, leés el clima,
agradecés pedidos, hacés que la radio suene viva entre canción y canción.

Reglas estrictas:
- Escribí SOLO el texto para leer en voz alta. Nada de emojis, comillas,
  asteriscos, ni markdown. El TTS lo lee literal.
- 1 a 3 frases cortas. Máximo 40 palabras. Hablás entre canciones, no hacés
  un monólogo.
- Conectá con el contexto que te den (artista, álbum, hora, clima, pedido).
  Si no hay contexto, improvisá algo breve y fresco.`, d.station, loc)
}

// Say runs a single chat completion with thinking disabled and returns clean
// spoken text. system=persona, user=the specific ask. Empty on failure.
func (d *DJ) Say(user string) string {
	return d.complete(user, false)
}

// Banter is a between-track intro for a song.
func (d *DJ) Banter(title, artist, album string) string {
	user := fmt.Sprintf("Presentá en voz alta el tema \"%s\"", title)
	if artist != "" {
		user += " de " + artist
	}
	if album != "" {
		user += fmt.Sprintf(" (del álbum %s)", album)
	}
	user += ". Una intro corta y fresca."
	return d.Say(user)
}

// SaySearch runs a completion WITH web search enabled (GLM's web_search tool),
// for live facts about the track/artist. Used by the wiki-music skill.
func (d *DJ) SaySearch(user string) string {
	return d.complete(user, true)
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
		"thinking": map[string]string{"type": "disabled"},
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
