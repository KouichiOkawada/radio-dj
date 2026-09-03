// Package status exposes what's on air + accepts listener requests.
// Endpoints: GET / (player UI), GET /now-playing, GET /health, POST /request.
package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Track is the on-air shape clients consume (Src is internal-only, never serialized).
type Track struct {
	Type           string  `json:"type,omitempty"` // music | dj | news
	Title          string  `json:"title"`
	Artist         string  `json:"artist"`
	Album          string  `json:"album,omitempty"`
	Year           string  `json:"year,omitempty"`
	BPM            string  `json:"bpm,omitempty"`
	Duration       float64 `json:"duration,omitempty"` // seconds (folder source only)
	Src            string  `json:"-"`                  // internal: source path for cover art
	AttributionURL string  `json:"attribution_url,omitempty"`
	LicenseURL     string  `json:"license_url,omitempty"`
	Source         string  `json:"source,omitempty"`
	Category       string  `json:"category,omitempty"`
	URL            string  `json:"url,omitempty"`
	Description    string  `json:"description,omitempty"`
	PublishedAt    string  `json:"published_at,omitempty"`
	ImageURL       string  `json:"image_url,omitempty"`
	SpeechText     string  `json:"speech_text,omitempty"`
}

type Request struct {
	From      string    `json:"from,omitempty"`
	Text      string    `json:"text"`
	AskedAt   time.Time `json:"askedAt"`
	Status    string    `json:"status"` // queued | matched | not-found
	MatchHint string    `json:"matchHint,omitempty"`
}

type NowPlaying struct {
	Mode           string    `json:"mode,omitempty"`
	Current        Track     `json:"current"`
	Next           *Track    `json:"next,omitempty"`
	History        []Track   `json:"history,omitempty"`
	Requests       []Request `json:"requests,omitempty"`
	Playing        bool      `json:"playing"`
	StartedAt      time.Time `json:"startedAt"`
	Listeners      int       `json:"listeners,omitempty"`
	NewsReady      bool      `json:"news_ready"`
	NewsReadyCount int       `json:"news_ready_count"`
	NewsState      string    `json:"news_state,omitempty"` // loading | ready | waiting | unavailable
	NewsPreview    *Track    `json:"news_preview,omitempty"`
	NewsPriority   string    `json:"news_priority,omitempty"`
	NewsSource     string    `json:"news_source,omitempty"`
	NewsSources    []string  `json:"news_sources,omitempty"`
	NextNewsKind   string    `json:"next_news_kind,omitempty"`
	NextNewsAt     string    `json:"next_news_at,omitempty"`
}

type Server struct {
	mu                  sync.RWMutex
	cur                 NowPlaying
	history             []Track // recently played, newest first
	dir                 string
	requests            []Request // raw, unresolved
	needsSetup          bool
	controlHandler      func(string) bool // radio-loop skip callback (POST /control); nil = no radio
	modeHandler         func(string) bool
	newsPriorityHandler func(string) bool
	newsSourceHandler   func(string) bool
	mode                string
	lang                string // config.Language ("es"|"en") — drives the index UI strings
	mount               string // icecast mount (e.g. /stream.aac) — drives reverse proxy + <audio> src
	// icecast admin API for listener counts (lazy, cached 3s)
	icBase      string
	icPw        string
	icListeners int
	icCacheT    time.Time
	subs        map[chan NowPlaying]struct{} // SSE subscribers for /events
}

func New(stateDir string, needsSetup bool, mount string) *Server {
	_ = os.MkdirAll(stateDir, 0o755)
	return &Server{dir: stateDir, needsSetup: needsSetup, mount: mount, subs: map[chan NowPlaying]struct{}{}}
}

// SetLanguage stores the UI language for the index template strings.
func (s *Server) SetLanguage(lang string) {
	s.lang = lang
}

// StateDir exposes the server-owned runtime directory to tightly scoped radio
// maintenance such as removing completed generated news audio.
func (s *Server) StateDir() string { return s.dir }

// SetControlHandler wires the radio loop's skip callback. POST /control
// forwards "previous"/"next" here; the handler kills the in-flight decoder
// and advances to the chosen track. nil handler → 409 (no track playing).
//
// Guarded by mu: the radio loop wires this only once the streamer is up, which
// is seconds after ListenAndServeHTTP already started serving — so a /control
// request can genuinely land mid-write.
func (s *Server) SetControlHandler(h func(string) bool) {
	s.mu.Lock()
	s.controlHandler = h
	s.mu.Unlock()
}

// SetModeHandler connects the UI mode switcher to the radio producer.
func (s *Server) SetModeHandler(mode string, h func(string) bool) {
	s.mu.Lock()
	s.mode = mode
	s.modeHandler = h
	s.mu.Unlock()
}

func (s *Server) SetNewsPriorityHandler(h func(string) bool) {
	s.mu.Lock()
	s.newsPriorityHandler = h
	s.mu.Unlock()
}

func (s *Server) SetNewsSourceHandler(h func(string) bool) {
	s.mu.Lock()
	s.newsSourceHandler = h
	s.mu.Unlock()
}

func (s *Server) SetNewsSources(sources []string) {
	s.mu.Lock()
	s.cur.NewsSources = append([]string(nil), sources...)
	s.mu.Unlock()
}

func (s *Server) SetNextNewsSlot(kind string, at time.Time) {
	if s == nil || at.IsZero() {
		return
	}
	atText := at.Format(time.RFC3339)
	s.mu.Lock()
	changed := s.cur.NextNewsKind != kind || s.cur.NextNewsAt != atText
	s.cur.NextNewsKind = kind
	s.cur.NextNewsAt = atText
	s.mu.Unlock()
	if changed {
		go s.broadcast()
	}
}

func (s *Server) setNewsPriority(category string) bool {
	s.mu.RLock()
	h := s.newsPriorityHandler
	s.mu.RUnlock()
	if h == nil || !h(category) {
		return false
	}
	s.mu.Lock()
	s.cur.NewsPriority = category
	s.mu.Unlock()
	go s.broadcast()
	return true
}

func (s *Server) setNewsSource(source string) bool {
	s.mu.RLock()
	h := s.newsSourceHandler
	s.mu.RUnlock()
	if h == nil || !h(source) {
		return false
	}
	s.mu.Lock()
	s.cur.NewsSource = source
	s.mu.Unlock()
	go s.broadcast()
	return true
}

func (s *Server) setMode(mode string) bool {
	s.mu.RLock()
	h := s.modeHandler
	s.mu.RUnlock()
	if h == nil || !h(mode) {
		return false
	}
	s.mu.Lock()
	s.mode = mode
	s.cur.Mode = mode
	s.mu.Unlock()
	go s.broadcast()
	return true
}

// requestControl forwards a control action to the radio loop. Returns whether
// a track was on air (accepted).
func (s *Server) requestControl(action string) bool {
	s.mu.RLock()
	h := s.controlHandler
	s.mu.RUnlock()
	if h == nil {
		return false
	}
	// Called outside mu on purpose: the handler blocks on the radio loop's own
	// mutex, and holding mu here would stall every SetCurrent/broadcast behind it.
	return h(action)
}

// SetCurrent publishes the current + next track (called by the radio loop as
// each track begins). The previous track is pushed into history and its cover
// art is extracted to cover.jpg in the background.
func (s *Server) SetCurrent(cur, next Track) {
	if cur.Type == "" {
		cur.Type = "music"
	}
	s.mu.Lock()
	if prev := s.cur.Current; prev.Title != "" && prev.Title != cur.Title {
		s.history = append([]Track{{Title: prev.Title, Artist: prev.Artist, Album: prev.Album, Year: prev.Year}}, s.history...)
		if len(s.history) > 10 {
			s.history = s.history[:10]
		}
	}
	s.cur.Current = cur
	s.cur.Next = nil
	if next.Title != "" {
		s.cur.Next = &next
	}
	s.cur.StartedAt = time.Now()
	src := cur.Src
	s.mu.Unlock()
	s.persist()
	// extract cover SYNCHRONOUSLY before broadcasting: otherwise the SSE
	// reaches the browser before ffmpeg rewrites cover.jpg, so /cover serves the
	// previous track's art (always one track behind). ~50-200ms once per track
	// — invisible, and streamer.Play hasn't started yet anyway.
	if src != "" {
		s.extractCover(src)
	}
	go s.broadcast()
}

// MarkPlaying flips the on-air flag (decorative — the UI reads it to spin reels).
func (s *Server) MarkPlaying(on bool) {
	s.mu.Lock()
	s.cur.Playing = on
	s.mu.Unlock()
}

// SetNewsReadiness exposes whether at least one completely rendered bulletin
// is waiting in the radio-side preload queue. The player uses this to keep the
// news-only mode unavailable until selecting it cannot force the listener to
// wait on RSS/TTS/LLM work.
func (s *Server) SetNewsReadiness(ready bool, count int, state string) {
	if count < 0 {
		count = 0
	}
	s.mu.Lock()
	changed := s.cur.NewsReady != ready || s.cur.NewsReadyCount != count || s.cur.NewsState != state
	s.cur.NewsReady = ready
	s.cur.NewsReadyCount = count
	s.cur.NewsState = state
	s.mu.Unlock()
	if changed {
		go s.broadcast()
	}
}

// SetNewsPreview keeps the news desk visible while music is on air. It is a
// preview only; SetCurrent remains the authoritative article at air-time.
func (s *Server) SetNewsPreview(preview Track) {
	s.mu.Lock()
	s.cur.NewsPreview = &preview
	s.mu.Unlock()
	go s.broadcast()
}

func (s *Server) NewsReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur.NewsReady
}

// AddRequest enqueues a listener request; returns it with an id-ish hint.
func (s *Server) AddRequest(from, text string) Request {
	r := Request{From: from, Text: text, AskedAt: time.Now(), Status: "queued"}
	s.mu.Lock()
	s.requests = append(s.requests, r)
	s.mu.Unlock()
	go s.broadcast()
	return r
}

// broadcast pushes the current now-playing snapshot to every SSE subscriber.
// Sends are non-blocking: a slow client is skipped and catches the next change.
func (s *Server) broadcast() {
	np := s.Current()
	s.mu.RLock()
	subs := make([]chan NowPlaying, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- np:
		default: // slow subscriber — drop, it'll catch the next broadcast
		}
	}
}

// DrainRequests returns and clears pending requests (the radio loop resolves
// them into tracks at the next chunk build).
func (s *Server) DrainRequests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.requests
	s.requests = nil
	return r
}

// Current snapshots the full now-playing payload (current + next + history +
// requests + listeners). Listener count is served from a 3s cache and refreshed
// asynchronously so a slow icecast never blocks the snapshot.
func (s *Server) Current() NowPlaying {
	s.mu.RLock()
	np := s.cur
	if len(s.history) > 0 {
		np.History = append([]Track(nil), s.history...)
	}
	if len(s.requests) > 0 {
		np.Requests = append([]Request(nil), s.requests...)
	}
	np.Listeners = s.icListeners
	np.Mode = s.mode
	stale := s.icBase != "" && time.Since(s.icCacheT) > 3*time.Second
	s.mu.RUnlock()
	if stale {
		go s.refreshListeners()
	}
	return np
}

// SetIcecast enables listener counting by pointing at the icecast admin API.
// baseURL is e.g. http://localhost:7702; adminPw is the <admin-password>.
func (s *Server) SetIcecast(baseURL, adminPw string) {
	s.mu.Lock()
	s.icBase = strings.TrimRight(baseURL, "/")
	s.icPw = adminPw
	s.mu.Unlock()
}

// refreshListeners fetches the global listener count from icecast /admin/stats
// (the first <listeners>N</listeners> in the XML is the mount's total — all we
// need for a single-mount station) and updates the cache.
func (s *Server) refreshListeners() {
	req, err := http.NewRequest("GET", s.icBase+"/admin/stats", nil)
	if err != nil {
		return
	}
	req.SetBasicAuth("admin", s.icPw)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	n := parseListeners(string(body))
	s.mu.Lock()
	s.icListeners = n
	s.icCacheT = time.Now()
	s.mu.Unlock()
}

func parseListeners(xml string) int {
	const tag = "<listeners>"
	i := strings.Index(xml, tag)
	if i < 0 {
		return 0
	}
	rest := xml[i+len(tag):]
	j := strings.Index(rest, "</listeners>")
	if j < 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(rest[:j]))
	return n
}

// extractCover pulls the embedded artwork (if any) of src into cover.jpg so /cover
// can serve it. Best-effort: no art or ffmpeg absent → /cover 404s, the UI keeps
// its radio glyph. Transcoded to JPEG for a uniform, predictable content type.
func (s *Server) extractCover(src string) {
	dst := filepath.Join(s.dir, "cover.jpg")
	if err := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-i", src, "-map", "0:v:0", "-vframes", "1", "-q:v", "3", dst).Run(); err != nil {
		_ = os.Remove(dst) // partial/empty → don't serve stale art
	}
}

// handleCover serves the current track's artwork with an ETag keyed to the
// title, so clients revalidate (and get a 304) until the song changes.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(s.dir, "cover.jpg"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	title := s.cur.Current.Title
	s.mu.RUnlock()
	etag := fmt.Sprintf(`"%x"`, fnv32(title))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=10")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func (s *Server) persist() {
	b, _ := json.MarshalIndent(s.Current(), "", "  ")
	_ = os.WriteFile(filepath.Join(s.dir, "now-playing.json"), b, 0o644)
}

// ListenAndServeHTTP mounts the endpoints and runs the server in background.
func (s *Server) ListenAndServeHTTP(port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/now-playing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(s.Current())
	})
	// /events — Server-Sent Events: push now-playing the instant it changes.
	// Replaces the client's 4s poll; gives instant metadata + native reconnect.
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)
		ch := make(chan NowPlaying, 8)
		s.mu.Lock()
		s.subs[ch] = struct{}{}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.subs, ch)
			s.mu.Unlock()
		}()
		// initial snapshot so a fresh client doesn't wait for the next change
		if b, err := json.Marshal(s.Current()); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case np := <-ch:
				if b, err := json.Marshal(np); err == nil {
					fmt.Fprintf(w, "data: %s\n\n", b)
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		}
	})
	// /dj-log — structured on-air feedback log (DJ speech, requests, clock).
	// radio.logDJ appends JSONL lines to dj-log.txt; we return the parsed
	// entries as JSON so the client renders without parsing a text format.
	mux.HandleFunc("/dj-log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		entries := ReadDJLog(filepath.Join(s.dir, "dj-log.txt"))
		if len(entries) > 400 {
			entries = entries[len(entries)-400:]
		}
		_ = json.NewEncoder(w).Encode(entries)
	})
	mux.HandleFunc("/font/permanent-marker.woff2", serveFont)
	registerPWA(mux)
	mux.HandleFunc("/cover", s.handleCover)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"on-air"}`))
	})
	mux.HandleFunc("/playback-mode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			s.mu.RLock()
			mode := s.mode
			s.mu.RUnlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"mode": mode})
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "GET or PUT only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Mode != "radio" && body.Mode != "music" && body.Mode != "news") {
			http.Error(w, `{"error":"mode must be radio, music, or news"}`, http.StatusBadRequest)
			return
		}
		if !s.setMode(body.Mode) {
			http.Error(w, `{"error":"mode switch unavailable"}`, http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"mode": body.Mode})
	})
	mux.HandleFunc("/news-priority", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]string{"category": s.Current().NewsPriority})
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "GET or PUT only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Category string `json:"category"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !s.setNewsPriority(body.Category) {
			http.Error(w, `{"error":"category must be stock, finance, general, hokkaido, tech, or empty"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"category": body.Category})
	})
	mux.HandleFunc("/news-source", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]string{"source": s.Current().NewsSource})
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "GET or PUT only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Source string `json:"source"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !s.setNewsSource(body.Source) {
			http.Error(w, `{"error":"source must be one of news_sources or empty"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"source": body.Source})
	})
	mux.HandleFunc("/request", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			From string `json:"from"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
			http.Error(w, `{"error":"missing text"}`, http.StatusBadRequest)
			return
		}
		req := s.AddRequest(body.From, body.Text)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(req)
	})
	// /control — skip/previous for the live broadcast. The handler kills the
	// in-flight music decoder (listeners stay connected); the radio loop then
	// advances to the next or previous track. 409 when nothing is on air.
	mux.HandleFunc("/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			(body.Action != "previous" && body.Action != "next") {
			http.Error(w, `{"error":"action must be 'previous' or 'next'"}`, http.StatusBadRequest)
			return
		}
		if !s.requestControl(body.Action) {
			writeJSON(w, http.StatusConflict, `{"error":"no track playing"}`)
			return
		}
		writeJSON(w, http.StatusAccepted, `{"ok":true}`)
	})
	mux.HandleFunc("/onboarding", func(w http.ResponseWriter, r *http.Request) {
		// Once configured, the wizard closes — /onboarding redirects to the
		// player. To reconfigure, edit config.json (or delete it) and restart.
		if !s.needsSetup {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_ = os.MkdirAll(s.dir, 0o755)
			if err := os.WriteFile(filepath.Join(s.dir, "config.json"), body, 0o600); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"restart":true}`))
			return
		}
		serveOnboarding(w)
	})
	mux.HandleFunc("/onboarding/test", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			BaseURL  string `json:"base_url"`
			APIKey   string `json:"api_key"`
			Model    string `json:"model"`
			Provider string `json:"provider"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.BaseURL == "" || req.APIKey == "" || req.Model == "" {
			writeJSON(w, 400, `{"ok":false,"error":"falta base_url, api_key o model"}`)
			return
		}
		body := map[string]any{
			"model":      req.Model,
			"messages":   []map[string]string{{"role": "user", "content": "Reply with the single word: OK"}},
			"max_tokens": 5,
		}
		if req.Provider == "glm" {
			body["thinking"] = map[string]string{"type": "disabled"}
		}
		raw, _ := json.Marshal(body)
		hr, _ := http.NewRequest("POST", strings.TrimRight(req.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
		hr.Header.Set("Authorization", "Bearer "+req.APIKey)
		hr.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(hr)
		if err != nil {
			writeJSON(w, 200, fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error()))
			return
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			writeJSON(w, 200, fmt.Sprintf(`{"ok":false,"error":"HTTP %d: %s"}`, resp.StatusCode, truncate(string(rb), 200)))
			return
		}
		writeJSON(w, 200, `{"ok":true}`)
	})
	// s.mount reverse-proxies icecast so the player works behind a single
	// origin (funnel/serve HTTPS) without mixed-content, AND on direct LAN.
	// FlushInterval=-1 streams the live audio without buffering. The path
	// follows cfg.IcecastMount, which the <audio> src mirrors via {{.StreamPath}}.
	if upstream, err := url.Parse("http://127.0.0.1:7702"); err == nil { // ponytail: icecast internal port is fixed
		rp := httputil.NewSingleHostReverseProxy(upstream)
		rp.FlushInterval = -1
		mux.Handle(s.mount, rp)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if s.needsSetup {
			http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
			return
		}
		s.serveIndex(w)
	})
	go func() {
		if err := http.ListenAndServe(":"+strconv.Itoa(port), mux); err != nil {
			log.Printf("[radio-dj] status server :%d: %v", port, err)
		}
	}()
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
