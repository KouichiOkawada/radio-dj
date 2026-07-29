// Package skills generates the DJ's between-track segments — ported from
// subwave's skill concept (station id, time, weather, curiosity, banter,
// request ack) but prompt-only + Open-Meteo for weather. Each Segment asks
// the DJ (GLM) to phrase ready-to-read Spanish, then returns it for TTS.
package skills

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"radio-dj/internal/dj"
	"radio-dj/internal/library"
)

type Ctx struct {
	Track       library.Track
	IsRequest   bool
	RequestText string
}

// Segue returns a rotating non-track segment (station id / time / weather /
// curiosity). Callers usually alternate: most tracks get an Intro, every few
// get a Segue to vary the break.
type Pool struct {
	station, location string
	lat, lon          float64
	i                 int
	skills            map[string]string // editable prompts from ~/.radio-dj/skills/*.md
}

func NewPool(station, location string, lat, lon float64, skills map[string]string) *Pool {
	return &Pool{station: station, location: location, lat: lat, lon: lon, skills: skills}
}

// prompt fills an editable skill template with values; falls back to default.
func (p *Pool) prompt(kind string, kv map[string]string) string {
	tmpl := p.skills[kind]
	if tmpl == "" {
		tmpl = defaultSkills[kind]
	}
	return substitute(tmpl, kv)
}

// Segue rotates through station-id → time → weather → curiosity → wiki.
// The track is used by the wiki segment (web-searched fact about the artist).
func (p *Pool) Segue(d *dj.DJ, t library.Track) string {
	kinds := []string{"station", "time", "weather", "curiosity", "wiki"}
	k := kinds[p.i%len(kinds)]
	p.i++
	switch k {
	case "station":
		return d.Say(p.prompt("station-id", map[string]string{"station": p.station, "location": p.location}))
	case "time":
		hm := time.Now().Format("15:04")
		return d.Say(p.prompt("time", map[string]string{"time": hm}))
	case "weather":
		return p.weather(d)
	case "curiosity":
		return d.Say(p.prompt("curiosity", nil))
	case "wiki":
		return WikiFact(d, t)
	}
	return ""
}

// WikiFact asks the DJ (with web search) for a real fact about the track's
// artist/song — pulled live from the web via GLM's web_search tool.
func WikiFact(d *dj.DJ, t library.Track) string {
	q := fmt.Sprintf("Tirá UN dato curioso o anecdota real, corto, sobre ")
	if t.Artist != "" {
		q += fmt.Sprintf("el artista %s", t.Artist)
		if t.Title != "" {
		q += fmt.Sprintf(" o la canción \"%s\"", t.Title)
		}
	} else if t.Title != "" {
		q += fmt.Sprintf("la canción \"%s\"", t.Title)
	} else {
		q += "algún artista o canción famosa"
	}
	q += ". Buscá en internet para que sea real y preciso. Una o dos frases, para leer al aire."
	return d.SaySearch(q)
}

// Intro is the standard between-track presentation of the next song.
func Intro(d *dj.DJ, t library.Track) string {
	return d.Banter(t.Title, t.Artist, t.Album)
}

// RequestAck thanks a listener's request before playing it.
func RequestAck(d *dj.DJ, t library.Track, req string) string {
	ask := fmt.Sprintf("Agradecé al aire un pedido del oyente: \"%s\". A continuación suena %s", req, t.Title)
	if t.Artist != "" {
		ask += " de " + t.Artist
	}
	ask += ". Corto y cálido."
	return d.Say(ask)
}

// weather fetches current conditions from Open-Meteo (free, no key) and asks
// the DJ to read them. Falls back to a neutral line if the API is unreachable.
func (p *Pool) weather(d *dj.DJ) string {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code&timezone=auto",
		p.lat, p.lon)
	resp, err := http.Get(url)
	if err != nil {
		return d.Say("Comentá brevemente cómo está el clima en " + p.location + ", sin inventar números.")
	}
	defer resp.Body.Close()
	var doc struct {
		Current struct {
			Temperature2m float64 `json:"temperature_2m"`
			WeatherCode   int     `json:"weather_code"`
		} `json:"current"`
	}
	if json.NewDecoder(resp.Body).Decode(&doc) != nil {
		return ""
	}
	desc := wmo(doc.Current.WeatherCode)
	return d.Say(fmt.Sprintf("Leé el clima al aire: en %s hay %d°C, %s. Breve, onda radio.",
		p.location, int(doc.Current.Temperature2m), desc))
}

// wmo maps WMO weather codes to a short Spanish description.
func wmo(c int) string {
	switch {
	case c == 0:
		return "cielo despejado"
	case c <= 3:
		return "algo de nubes"
	case c <= 48:
		return "niebla"
	case c <= 67:
		return "lluvia"
	case c <= 77:
		return "nieve"
	case c <= 82:
		return "chubascos"
	case c <= 99:
		return "tormenta"
	}
	return "cambio de tiempo"
}

// LoadDir reads editable skill prompts from <dir>/skills/*.md. Each file's
// content is a prompt template (placeholders: {station} {location} {title}// {artist} {time}); the filename (without .md) is the skill kind. Missing
// skills fall back to the built-in prompts. If the dir is empty, the defaults
// are seeded so users can edit them.
var defaultSkills = map[string]string{
	"station-id": "Hacé una identificación de la radio al aire: nombre {station}, transmitiendo desde {location}. Corta, estilo FM.",
	"time":       "Decí la hora al aire: son las {time}. Breve, onda radio.",
	"curiosity":  "Tirá un dato curioso corto sobre música, sobre el artista que suena, o sobre el día de hoy. Una sola línea, fresca.",
}

func LoadDir(dir string) map[string]string {
	skillsDir := filepath.Join(dir, "skills")
	out := map[string]string{}
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		// first run: seed the defaults so they're editable
		_ = os.MkdirAll(skillsDir, 0o755)
		for k, v := range defaultSkills {
			_ = os.WriteFile(filepath.Join(skillsDir, k+".md"), []byte(v), 0o644)
			out[k] = v
		}
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		kind := strings.TrimSuffix(e.Name(), ".md")
		b, err := os.ReadFile(filepath.Join(skillsDir, e.Name()))
		if err == nil {
			out[kind] = string(b)
		}
	}
	return out
}

// substitute fills {placeholders} in a prompt template.
func substitute(tmpl string, kv map[string]string) string {
	for k, v := range kv {
		tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
	}
	return tmpl
}

// pick random helper kept for future variety picks.
var _ = rand.Intn
var _ = fmt.Sprintf
var _ = time.Now
var _ = json.Marshal
var _ = http.Get
