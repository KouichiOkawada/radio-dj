// Package config loads radio-dj runtime config from three layers, lowest wins:
// defaults < config file (~/.radio-dj/config.json, written by the onboarding
// wizard) < environment. The wizard persists a baseline; any RDJ_* env var
// still overrides it for power users / CI.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"radio-dj/internal/codec"
)

type Config struct {
	Source          string // "folder" | "navidrome"
	Library         string
	NavidromeURL    string
	NavidromeUser   string
	NavidromePass   string
	IcecastHost     string
	IcecastPort     int
	IcecastSourcePW string
	IcecastMount    string
	Encoder         string // ffmpeg audio encoder (aac_at | aac | libmp3lame) — drives format + mount
	StationName     string
	Bitrate         int
	Chunk           int

	DJEnabled       bool
	DJEvery         int    // legacy: soft floor of songs between talks. Cadence is now LLM-driven.
	DJTalk          string // poco | regular | mucho | verboso — how chatty the director is
	GLMBaseURL      string
	GLMAPIKey       string
	GLMModel        string
	LLMProvider     string // preset name (glm/openai/openrouter/...)
	VoiceProvider   string // edge-tts | piper | say
	Voice           string // voice id for the provider
	VoiceCmd        string // raw override (power users); empty = use provider+voice
	Bed             string
	NewsEvery       int
	NewsFeeds       []NewsFeed
	NewsMaxAgeHours int
	NewsBGMPath     string
	NewsBGMVolume   float64
	Audio           AudioSettings
	PlayMode        string

	LocationName string
	Latitude     float64
	Longitude    float64
	Language     string // "es" | "en"

	StatusPort int
	StateDir   string
}

// NewsFeed is an RSS or Atom endpoint used for the attributed news bulletin.
type NewsFeed struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// AudioSettings are persisted in config.json and constrained to linear gain
// values, where 0 is silent and 1 is unity gain.
type AudioSettings struct {
	MasterVolume  float64 `json:"master_volume"`
	MusicVolume   float64 `json:"music_volume"`
	VoiceVolume   float64 `json:"voice_volume"`
	NewsBGMVolume float64 `json:"news_bgm_volume"`
}

// fileConfig is the onboarding-persisted shape (user-settable fields only).
type fileConfig struct {
	Library         string        `json:"library,omitempty"`
	Source          string        `json:"source,omitempty"`
	NavidromeURL    string        `json:"navidrome_url,omitempty"`
	NavidromeUser   string        `json:"navidrome_user,omitempty"`
	NavidromePass   string        `json:"navidrome_pass,omitempty"`
	GLMAPIKey       string        `json:"glm_api_key,omitempty"`
	GLMBaseURL      string        `json:"glm_base_url,omitempty"`
	GLMModel        string        `json:"glm_model,omitempty"`
	LLMProvider     string        `json:"llm_provider,omitempty"`
	VoiceProvider   string        `json:"voice_provider,omitempty"`
	Voice           string        `json:"voice,omitempty"`
	VoiceCmd        string        `json:"voice_cmd,omitempty"`
	Location        string        `json:"location,omitempty"`
	Latitude        float64       `json:"lat,omitempty"`
	Longitude       float64       `json:"lon,omitempty"`
	Language        string        `json:"language,omitempty"`
	DJEvery         int           `json:"dj_every,omitempty"`
	DJTalk          string        `json:"dj_talk,omitempty"`
	Chunk           int           `json:"chunk,omitempty"`
	Bitrate         int           `json:"bitrate,omitempty"`
	StationName     string        `json:"station_name,omitempty"`
	Bed             string        `json:"bed,omitempty"`
	NewsEvery       int           `json:"news_every,omitempty"`
	NewsFeeds       []NewsFeed    `json:"news_feeds,omitempty"`
	NewsMaxAgeHours int           `json:"news_max_age_hours,omitempty"`
	NewsBGMPath     string        `json:"news_bgm_path,omitempty"`
	NewsBGMVolume   float64       `json:"news_bgm_volume,omitempty"`
	Audio           AudioSettings `json:"audio,omitempty"`
	PlayMode        string        `json:"play_mode,omitempty"`
}

// normalizeTalk canonicalizes the talkiness dial to English keys, accepting
// the Spanish aliases for back-comat (early configs used "verboso"). Unknown
// or empty → "regular". The director prompt describes these four keys.
func normalizeTalk(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "poco", "low", "bajo", "baja":
		return "low"
	case "mucho", "high", "alto", "alta":
		return "high"
	case "verboso", "verbose":
		return "verbose"
	default: // "", "regular", "medio", "normal", typos
		return "regular"
	}
}

func Load() Config {
	dir := defaultStateDir()
	f := loadFile(dir)
	enc := codec.Resolve()
	mount := getenv("RDJ_ICECAST_MOUNT", "/stream"+codec.MetaFor(enc).Suffix)
	c := Config{
		Source:          pick("RDJ_SOURCE", f.Source, "folder"),
		Library:         pick("RDJ_LIBRARY", f.Library, os.Getenv("HOME")+"/Music/library"),
		NavidromeURL:    pick("RDJ_NAVIDROME_URL", f.NavidromeURL, "http://localhost:4533"),
		NavidromeUser:   pickEnvOrFile("RDJ_NAVIDROME_USER", f.NavidromeUser),
		NavidromePass:   pickEnvOrFile("RDJ_NAVIDROME_PASS", f.NavidromePass),
		IcecastHost:     getenv("RDJ_ICECAST_HOST", "localhost"),
		IcecastPort:     getint("RDJ_ICECAST_PORT", 7702),
		IcecastSourcePW: os.Getenv("RDJ_ICECAST_SOURCE_PW"),
		IcecastMount:    mount,
		Encoder:         enc,
		StationName:     pick("RDJ_STATION_NAME", f.StationName, "radio-dj"),
		Bitrate:         pickInt("RDJ_BITRATE", f.Bitrate, 192),
		Chunk:           pickInt("RDJ_CHUNK", f.Chunk, 8),
		DJEvery:         pickInt("RDJ_DJ_EVERY", f.DJEvery, 3),
		DJTalk:          normalizeTalk(pick("RDJ_DJ_TALK", f.DJTalk, "regular")),
		GLMBaseURL:      pick("RDJ_GLM_BASE_URL", f.GLMBaseURL, "https://api.z.ai/api/coding/paas/v4"),
		GLMAPIKey:       firstNonEmpty(os.Getenv("RDJ_GLM_API_KEY"), f.GLMAPIKey, os.Getenv("ZAI_API_KEY")),
		GLMModel:        pick("RDJ_GLM_MODEL", f.GLMModel, "glm-5.2"),
		LLMProvider:     pick("RDJ_LLM_PROVIDER", f.LLMProvider, "glm"),
		VoiceProvider:   pick("RDJ_VOICE_PROVIDER", f.VoiceProvider, ""),
		Voice:           pick("RDJ_VOICE", f.Voice, ""),
		VoiceCmd:        firstNonEmpty(os.Getenv("RDJ_VOICE_CMD"), f.VoiceCmd),
		Bed:             firstNonEmpty(os.Getenv("RDJ_BED"), f.Bed),
		NewsEvery:       pickInt("RDJ_NEWS_EVERY", f.NewsEvery, 0),
		NewsFeeds:       f.NewsFeeds,
		NewsMaxAgeHours: pickInt("RDJ_NEWS_MAX_AGE_HOURS", f.NewsMaxAgeHours, 72),
		NewsBGMPath:     pick("RDJ_NEWS_BGM_PATH", f.NewsBGMPath, `C:\Radio\assets\news-bed.mp3`),
		NewsBGMVolume:   pickFloat("RDJ_NEWS_BGM_VOLUME", f.NewsBGMVolume, 0.35),
		Audio:           normalizeAudio(f.Audio, f.NewsBGMVolume),
		PlayMode:        normalizePlayMode(pick("RDJ_PLAY_MODE", f.PlayMode, "radio")),
		LocationName:    pick("RDJ_LOCATION", f.Location, "La Paz"),
		Latitude:        pickFloat("RDJ_LAT", f.Latitude, -16.5),
		Longitude:       pickFloat("RDJ_LON", f.Longitude, -68.15),
		Language:        pick("RDJ_LANGUAGE", f.Language, "es"),
		StatusPort:      getint("RDJ_STATUS_PORT", 7710),
		StateDir:        getenv("RDJ_STATE_DIR", dir),
	}
	// Ollama runs locally and does not require an API key. Other providers keep
	// the existing key requirement so an accidental cloud configuration cannot
	// make unauthenticated requests.
	llmReady := c.GLMAPIKey != "" || strings.EqualFold(c.LLMProvider, "ollama")
	c.DJEnabled = llmReady && (c.VoiceCmd != "" || c.VoiceProvider != "")
	return c
}

func normalizePlayMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "music", "news", "radio":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "radio"
	}
}

// SavePlaybackMode updates only the persisted playback mode, preserving all
// other user settings in config.json.
func SavePlaybackMode(dir, mode string) error {
	mode = normalizePlayMode(mode)
	path := filepath.Join(dir, "config.json")
	var raw map[string]json.RawMessage
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(mode)
	raw["play_mode"] = b
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func normalizeAudio(a AudioSettings, legacyNews float64) AudioSettings {
	if a.MasterVolume == 0 {
		a.MasterVolume = 0.8
	}
	if a.MusicVolume == 0 {
		a.MusicVolume = 0.9
	}
	if a.VoiceVolume == 0 {
		a.VoiceVolume = 1
	}
	if a.NewsBGMVolume == 0 {
		a.NewsBGMVolume = legacyNews
		if a.NewsBGMVolume == 0 {
			a.NewsBGMVolume = 0.35
		}
	}
	return a
}

// NeedsSetup reports whether the first-run wizard still needs to be shown.
// A saved config is enough to complete setup: AI and voice are optional, and
// music-only stations must still be able to open the player UI.
func (c Config) NeedsSetup() bool {
	if _, err := os.Stat(filepath.Join(c.StateDir, "config.json")); err == nil {
		return false
	}
	if c.GLMAPIKey == "" && !strings.EqualFold(c.LLMProvider, "ollama") {
		return true
	}
	return c.VoiceProvider == "" && c.VoiceCmd == ""
}

// loadFile reads the persisted config.json (empty struct if absent).
func loadFile(dir string) fileConfig {
	var f fileConfig
	if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	return f
}

// SaveFile persists the onboarding wizard input to config.json.
func SaveFile(dir string, f fileConfig) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), b, 0o644)
}

// pick returns env > file > default.
func pick(envKey, fileVal, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if fileVal != "" {
		return fileVal
	}
	return def
}
func pickEnvOrFile(envKey, fileVal string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fileVal
}
func pickInt(envKey string, fileVal, def int) int {
	if v := os.Getenv(envKey); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	if fileVal != 0 {
		return fileVal
	}
	return def
}
func pickFloat(envKey string, fileVal, def float64) float64 {
	if v := os.Getenv(envKey); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	if fileVal != 0 {
		return fileVal
	}
	return def
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// defaultStateDir is the standalone state directory (~/.radio-dj).
func defaultStateDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".radio-dj")
	}
	return filepath.Join(os.Getenv("HOME"), ".radio-dj")
}
