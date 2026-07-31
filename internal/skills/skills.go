// Package skills generates the DJ's between-track segments — station id, time,
// weather, curiosity, banter, request ack. The editable skill flavor (station-id
// / time / curiosity prompts) lives as .md files in the state dir, seeded from
// internal/i18n/skills/{lang}/*.md on first boot. All spoken phrasing is owned
// by the DJ (internal/dj), which reads it from the localized prompt JSON.
package skills

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"radio-dj/internal/dj"
	"radio-dj/internal/i18n"
	"radio-dj/internal/library"
)

type Ctx struct {
	Track       library.Track
	IsRequest   bool
	RequestText string
}

// Segue rotates through station-id → time → weather → curiosity → wiki.
// The track is used by the wiki segment (web-searched fact about the artist).
type Pool struct {
	station, location string
	lat, lon          float64
	i                 int
	skills            map[string]string // editable prompts from <state>/skills/*.md
}

func NewPool(station, location string, lat, lon float64, skills map[string]string) *Pool {
	return &Pool{station: station, location: location, lat: lat, lon: lon, skills: skills}
}

// prompt fills an editable skill template with values. Returns "" if the skill
// isn't loaded (the DJ then improvises from its system persona).
func (p *Pool) prompt(kind string, kv map[string]string) string {
	tmpl := p.skills[kind]
	if tmpl == "" {
		return ""
	}
	return substitute(tmpl, kv)
}

// Segue rotates through station-id → time → weather → curiosity → wiki.
// Returns the spoken text plus isTime=true when the clock skill is next: the
// caller MUST generate that one at air-time (not in the producer), otherwise
// the announced hour is stale by a whole tanda. See serve.go consumer loop.
func (p *Pool) Segue(d *dj.DJ, t library.Track) (text string, isTime bool) {
	kinds := []string{"station", "time", "weather", "curiosity", "wiki"}
	k := kinds[p.i%len(kinds)]
	p.i++
	switch k {
	case "station":
		return d.Say(p.prompt("station-id", map[string]string{"station": p.station, "location": p.location})), false
	case "time":
		// deferred — the clock is captured at air-time by the consumer.
		return "", true
	case "weather":
		return p.weather(d), false
	case "curiosity":
		return d.Say(p.prompt("curiosity", nil)), false
	case "wiki":
		return d.SayWiki(t.Artist, t.Title), false
	}
	return "", false
}

// Prompt fills a skill template with values (exported so the radio loop can
// build the time prompt at air-time, after the producer deferred it).
func (p *Pool) Prompt(kind string, kv map[string]string) string {
	return p.prompt(kind, kv)
}

// Intro is the standard between-track presentation of the next song.
func Intro(d *dj.DJ, t library.Track) string {
	return d.Banter(t.Title, t.Artist, t.Album)
}

// RequestAck thanks a listener's request before playing it.
func RequestAck(d *dj.DJ, t library.Track, from, req string) string {
	return d.SayRequest(t.Title, t.Artist, from, req)
}

// weather fetches current conditions from Open-Meteo (free, no key) and asks
// the DJ to read them. Falls back to a generic ask if the API is unreachable.
func (p *Pool) weather(d *dj.DJ) string {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code&timezone=auto",
		p.lat, p.lon)
	resp, err := http.Get(url)
	if err != nil {
		return d.SayWeather(p.location, "", 0, false)
	}
	defer resp.Body.Close()
	var doc struct {
		Current struct {
			Temperature2m float64 `json:"temperature_2m"`
			WeatherCode   int     `json:"weather_code"`
		} `json:"current"`
	}
	if json.NewDecoder(resp.Body).Decode(&doc) != nil {
		return d.SayWeather(p.location, "", 0, false)
	}
	return d.SayWeather(p.location, wmoCategory(doc.Current.WeatherCode), int(doc.Current.Temperature2m), true)
}

// wmoCategory maps a WMO weather code to a language-neutral category key.
// The localized wording lives in i18n prompts as wmo_<category>.
func wmoCategory(c int) string {
	switch {
	case c == 0:
		return "clear"
	case c <= 3:
		return "clouds"
	case c <= 48:
		return "fog"
	case c <= 67:
		return "rain"
	case c <= 77:
		return "snow"
	case c <= 82:
		return "showers"
	case c <= 99:
		return "storm"
	}
	return "change"
}

// LoadDir seeds the default skill prompts for lang into <dir>/skills/ (from
// the embedded i18n defaults), then reads every .md there into a map. Users
// edit the seeded files to customize the DJ's flavor — no recompile needed.
func LoadDir(dir, lang string) map[string]string {
	_ = i18n.SeedSkills(lang, dir) // best-effort; existing user files are kept
	skillsDir := filepath.Join(dir, "skills")
	out := map[string]string{}
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		kind := strings.TrimSuffix(e.Name(), ".md")
		if b, rerr := os.ReadFile(filepath.Join(skillsDir, e.Name())); rerr == nil {
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
