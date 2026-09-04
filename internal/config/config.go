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

	DJEnabled        bool
	DJEvery          int    // legacy: soft floor of songs between talks. Cadence is now LLM-driven.
	DJTalk           string // poco | regular | mucho | verboso — how chatty the director is
	GLMBaseURL       string
	GLMAPIKey        string
	GLMModel         string
	LLMProvider      string // preset name (glm/openai/openrouter/...)
	VoiceProvider    string // edge-tts | piper | say
	Voice            string // voice id for the provider
	VoiceCmd         string // raw override (power users); empty = use provider+voice
	Bed              string
	NewsEvery        int
	NewsFeeds        []NewsFeed
	NewsMaxAgeHours  int
	NewsBGMPath      string
	NewsBGMVolume    float64
	JQuantsAPIKey    string
	WatchSymbols     []string
	NewsExcludeTerms []string
	Audio            AudioSettings
	PlayMode         string
	AutoMusicEnabled bool
	AutoMusicTempDir string

	LocationName string
	Latitude     float64
	Longitude    float64
	Language     string // "es" | "en"

	StatusPort int
	StateDir   string
}

// NewsFeed is an RSS or Atom endpoint used for the attributed news bulletin.
type NewsFeed struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category,omitempty"`
}

// DefaultNewsFeeds is deliberately a diverse, account-free baseline.  A
// persisted configuration may add private feeds, but it should never leave the
// station dependent on one publisher whose RSS endpoint has gone stale.
// Every endpoint below is a first-party RSS/Atom feed, except Google News,
// which is only a supplementary regional discovery source.
func DefaultNewsFeeds() []NewsFeed {
	return []NewsFeed{
		{Name: "Yahoo!ニュース 主要", URL: "https://news.yahoo.co.jp/rss/topics/top-picks.xml", Category: "general"},
		{Name: "Yahoo!ニュース 国内", URL: "https://news.yahoo.co.jp/rss/categories/domestic.xml", Category: "general"},
		{Name: "Yahoo!ニュース 国際", URL: "https://news.yahoo.co.jp/rss/categories/world.xml", Category: "general"},
		{Name: "Yahoo!ニュース 経済", URL: "https://news.yahoo.co.jp/rss/categories/business.xml", Category: "finance"},
		{Name: "Google News 株式（補助）", URL: "https://news.google.com/rss/search?q=%22%E6%A0%AA%E5%BC%8F%E5%B8%82%E5%A0%B4%22%20OR%20%22%E6%97%A5%E7%B5%8C%E5%B9%B3%E5%9D%87%22%20OR%20TOPIX%20OR%20%E6%9D%B1%E8%A8%BC%20OR%20%E5%80%8B%E5%88%A5%E6%A0%AA&hl=ja&gl=JP&ceid=JP:ja", Category: "stock"},
		{Name: "Yahoo!ニュース IT", URL: "https://news.yahoo.co.jp/rss/categories/it.xml", Category: "tech"},
		{Name: "Yahoo!ニュース 科学", URL: "https://news.yahoo.co.jp/rss/categories/science.xml", Category: "tech"},
		{Name: "ITmedia AI+", URL: "https://rss.itmedia.co.jp/rss/2.0/aiplus.xml", Category: "tech"},
		{Name: "ITmedia NEWS", URL: "https://rss.itmedia.co.jp/rss/2.0/news_bursts.xml", Category: "tech"},
		{Name: "札幌市 報道発表", URL: "https://www.city.sapporo.jp/somu/koho/hodo/houdou.xml", Category: "hokkaido"},
		{Name: "札幌市 新着", URL: "https://www.city.sapporo.jp/new/shinchaku.xml", Category: "hokkaido"},
		{Name: "Google News 北海道（補助）", URL: "https://news.google.com/rss/search?q=%E5%8C%97%E6%B5%B7%E9%81%93&hl=ja&gl=JP&ceid=JP:ja", Category: "hokkaido"},
	}
}

// MergeNewsFeeds keeps the user's feeds and fills in the maintained baseline.
// It is URL-deduplicated so legacy configs remain compatible and no feed is
// fetched twice.  This also lets an old, stale configured feed fail without
// starving the rest of the news engine.
func MergeNewsFeeds(configured []NewsFeed) []NewsFeed {
	seen := make(map[string]bool, len(configured))
	out := make([]NewsFeed, 0, len(configured)+len(DefaultNewsFeeds()))
	for _, feed := range configured {
		feed.URL = strings.TrimSpace(feed.URL)
		if feed.URL == "" || seen[feed.URL] {
			continue
		}
		seen[feed.URL] = true
		out = append(out, feed)
	}
	for _, feed := range DefaultNewsFeeds() {
		if seen[feed.URL] {
			continue
		}
		seen[feed.URL] = true
		out = append(out, feed)
	}
	return out
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
	Library          string        `json:"library,omitempty"`
	Source           string        `json:"source,omitempty"`
	NavidromeURL     string        `json:"navidrome_url,omitempty"`
	NavidromeUser    string        `json:"navidrome_user,omitempty"`
	NavidromePass    string        `json:"navidrome_pass,omitempty"`
	GLMAPIKey        string        `json:"glm_api_key,omitempty"`
	GLMBaseURL       string        `json:"glm_base_url,omitempty"`
	GLMModel         string        `json:"glm_model,omitempty"`
	LLMProvider      string        `json:"llm_provider,omitempty"`
	VoiceProvider    string        `json:"voice_provider,omitempty"`
	Voice            string        `json:"voice,omitempty"`
	VoiceCmd         string        `json:"voice_cmd,omitempty"`
	Location         string        `json:"location,omitempty"`
	Latitude         float64       `json:"lat,omitempty"`
	Longitude        float64       `json:"lon,omitempty"`
	Language         string        `json:"language,omitempty"`
	DJEvery          int           `json:"dj_every,omitempty"`
	DJTalk           string        `json:"dj_talk,omitempty"`
	Chunk            int           `json:"chunk,omitempty"`
	Bitrate          int           `json:"bitrate,omitempty"`
	StationName      string        `json:"station_name,omitempty"`
	Bed              string        `json:"bed,omitempty"`
	NewsEvery        int           `json:"news_every,omitempty"`
	NewsFeeds        []NewsFeed    `json:"news_feeds,omitempty"`
	NewsMaxAgeHours  int           `json:"news_max_age_hours,omitempty"`
	NewsBGMPath      string        `json:"news_bgm_path,omitempty"`
	NewsBGMVolume    float64       `json:"news_bgm_volume,omitempty"`
	JQuantsAPIKey    string        `json:"jquants_api_key,omitempty"`
	WatchSymbols     []string      `json:"watch_symbols,omitempty"`
	NewsExcludeTerms []string      `json:"news_exclude_terms,omitempty"`
	Audio            AudioSettings `json:"audio,omitempty"`
	PlayMode         string        `json:"play_mode,omitempty"`
	AutoMusicEnabled *bool         `json:"auto_music_enabled,omitempty"`
	AutoMusicTempDir string        `json:"auto_music_temp_dir,omitempty"`
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
		Source:           pick("RDJ_SOURCE", f.Source, "folder"),
		Library:          pick("RDJ_LIBRARY", f.Library, os.Getenv("HOME")+"/Music/library"),
		NavidromeURL:     pick("RDJ_NAVIDROME_URL", f.NavidromeURL, "http://localhost:4533"),
		NavidromeUser:    pickEnvOrFile("RDJ_NAVIDROME_USER", f.NavidromeUser),
		NavidromePass:    pickEnvOrFile("RDJ_NAVIDROME_PASS", f.NavidromePass),
		IcecastHost:      getenv("RDJ_ICECAST_HOST", "localhost"),
		IcecastPort:      getint("RDJ_ICECAST_PORT", 7702),
		IcecastSourcePW:  os.Getenv("RDJ_ICECAST_SOURCE_PW"),
		IcecastMount:     mount,
		Encoder:          enc,
		StationName:      pick("RDJ_STATION_NAME", f.StationName, "radio-dj"),
		Bitrate:          pickInt("RDJ_BITRATE", f.Bitrate, 192),
		Chunk:            pickInt("RDJ_CHUNK", f.Chunk, 8),
		DJEvery:          pickInt("RDJ_DJ_EVERY", f.DJEvery, 3),
		DJTalk:           normalizeTalk(pick("RDJ_DJ_TALK", f.DJTalk, "regular")),
		GLMBaseURL:       pick("RDJ_GLM_BASE_URL", f.GLMBaseURL, "https://api.z.ai/api/coding/paas/v4"),
		GLMAPIKey:        firstNonEmpty(os.Getenv("RDJ_GLM_API_KEY"), f.GLMAPIKey, os.Getenv("ZAI_API_KEY")),
		GLMModel:         pick("RDJ_GLM_MODEL", f.GLMModel, "glm-5.2"),
		LLMProvider:      pick("RDJ_LLM_PROVIDER", f.LLMProvider, "glm"),
		VoiceProvider:    pick("RDJ_VOICE_PROVIDER", f.VoiceProvider, ""),
		Voice:            pick("RDJ_VOICE", f.Voice, ""),
		VoiceCmd:         firstNonEmpty(os.Getenv("RDJ_VOICE_CMD"), f.VoiceCmd),
		Bed:              firstNonEmpty(os.Getenv("RDJ_BED"), f.Bed),
		NewsEvery:        pickInt("RDJ_NEWS_EVERY", f.NewsEvery, 0),
		NewsFeeds:        MergeNewsFeeds(f.NewsFeeds),
		NewsMaxAgeHours:  pickInt("RDJ_NEWS_MAX_AGE_HOURS", f.NewsMaxAgeHours, 6),
		NewsBGMPath:      pick("RDJ_NEWS_BGM_PATH", f.NewsBGMPath, `C:\Radio\assets\news-bed.mp3`),
		NewsBGMVolume:    pickFloat("RDJ_NEWS_BGM_VOLUME", f.NewsBGMVolume, 0.15),
		JQuantsAPIKey:    firstNonEmpty(os.Getenv("JQUANTS_API_KEY"), f.JQuantsAPIKey),
		WatchSymbols:     append([]string(nil), f.WatchSymbols...),
		NewsExcludeTerms: append([]string(nil), f.NewsExcludeTerms...),
		Audio:            normalizeAudio(f.Audio, f.NewsBGMVolume),
		PlayMode:         normalizePlayMode(pick("RDJ_PLAY_MODE", f.PlayMode, "radio")),
		AutoMusicEnabled: pickOptionalBool("RDJ_AUTO_MUSIC", f.AutoMusicEnabled, true),
		AutoMusicTempDir: pick("RDJ_AUTO_MUSIC_TEMP_DIR", f.AutoMusicTempDir, filepath.Join(pick("RDJ_LIBRARY", f.Library, os.Getenv("HOME")+"/Music/library"), "temporary")),
		LocationName:     pick("RDJ_LOCATION", f.Location, "La Paz"),
		Latitude:         pickFloat("RDJ_LAT", f.Latitude, -16.5),
		Longitude:        pickFloat("RDJ_LON", f.Longitude, -68.15),
		Language:         pick("RDJ_LANGUAGE", f.Language, "es"),
		StatusPort:       getint("RDJ_STATUS_PORT", 7710),
		StateDir:         getenv("RDJ_STATE_DIR", dir),
	}
	c.NewsBGMPath = resolveMovedLibraryFile(c.NewsBGMPath, c.Library)

	// Ollama deliberately skips the heavyweight structured director. In radio
	// mode its batch boundary is therefore also the deterministic news cadence.
	// Aligning the batch with news_every makes "3" actually mean roughly every
	// three songs instead of only matching occasionally against an 8-song chunk.
	if strings.EqualFold(c.LLMProvider, "ollama") && c.PlayMode == "radio" && c.NewsEvery > 0 {
		c.Chunk = c.NewsEvery
	}

	// Ollama runs locally and does not require an API key. Other providers keep
	// the existing key requirement so an accidental cloud configuration cannot
	// make unauthenticated requests.
	llmReady := c.GLMAPIKey != "" || strings.EqualFold(c.LLMProvider, "ollama")
	c.DJEnabled = llmReady && (c.VoiceCmd != "" || c.VoiceProvider != "")
	return c
}

// resolveMovedLibraryFile keeps older configs working after the installer
// separated permanent songs from disposable downloads. Only an existing file
// with the same basename under the library's permanent directory is accepted.
func resolveMovedLibraryFile(path, libraryRoot string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	candidate := filepath.Join(libraryRoot, "permanent", filepath.Base(path))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return path
}

func pickOptionalBool(envKey string, fileVal *bool, def bool) bool {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	if fileVal != nil {
		return *fileVal
	}
	return def
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
			a.NewsBGMVolume = 0.15
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
