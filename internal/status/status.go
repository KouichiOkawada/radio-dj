// Package status exposes what's on air + accepts listener requests.
// Endpoints: GET / (player UI), GET /now-playing, GET /health, POST /request.
package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Track is the on-air shape clients consume (no internal paths leaked).
type Track struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album,omitempty"`
}

type Request struct {
	Text      string    `json:"text"`
	AskedAt   time.Time `json:"askedAt"`
	Status    string    `json:"status"` // queued | matched | not-found
	MatchHint string    `json:"matchHint,omitempty"`
}

type NowPlaying struct {
	Current  Track     `json:"current"`
	Next     *Track    `json:"next,omitempty"`
	Requests []Request `json:"requests,omitempty"`
	Playing  bool      `json:"playing"`
	StartedAt time.Time `json:"startedAt"`
}

type Server struct {
	mu          sync.RWMutex
	cur         NowPlaying
	dir         string
	requests    []Request // raw, unresolved
	needsSetup  bool
}

func New(stateDir string, needsSetup bool) *Server {
	_ = os.MkdirAll(stateDir, 0o755)
	return &Server{dir: stateDir, needsSetup: needsSetup}
}

// SetCurrent publishes the current + next track (called by the radio loop as
// each track begins).
func (s *Server) SetCurrent(cur, next Track) {
	s.mu.Lock()
	s.cur.Current = cur
	s.cur.Next = nil
	if next.Title != "" {
		s.cur.Next = &next
	}
	s.cur.StartedAt = time.Now()
	s.mu.Unlock()
	s.persist()
}

// MarkPlaying flips the on-air flag (decorative — the UI reads it to spin reels).
func (s *Server) MarkPlaying(on bool) {
	s.mu.Lock()
	s.cur.Playing = on
	s.mu.Unlock()
}

// AddRequest enqueues a listener request; returns it with an id-ish hint.
func (s *Server) AddRequest(text string) Request {
	r := Request{Text: text, AskedAt: time.Now(), Status: "queued"}
	s.mu.Lock()
	s.requests = append(s.requests, r)
	s.mu.Unlock()
	return r
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

// Current snapshots the full now-playing payload (current + next + requests).
func (s *Server) Current() NowPlaying {
	s.mu.RLock()
	defer s.mu.RUnlock()
	np := s.cur
	if len(s.requests) > 0 {
		np.Requests = append([]Request(nil), s.requests...)
	}
	return np
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
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"on-air"}`))
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
		var body struct{ Text string `json:"text"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
			http.Error(w, `{"error":"missing text"}`, http.StatusBadRequest)
			return
		}
		req := s.AddRequest(body.Text)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(req)
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
			BaseURL string `json:"base_url"`
			APIKey  string `json:"api_key"`
			Model   string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.BaseURL == "" || req.APIKey == "" || req.Model == "" {
			writeJSON(w, 400, `{"ok":false,"error":"falta base_url, api_key o model"}`)
			return
		}
		body, _ := json.Marshal(map[string]any{
			"model": req.Model,
			"messages": []map[string]string{{"role": "user", "content": "Reply with the single word: OK"}},
			"max_tokens": 5, "thinking": map[string]string{"type": "disabled"},
		})
		hr, _ := http.NewRequest("POST", strings.TrimRight(req.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if s.needsSetup {
			http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
			return
		}
		serveIndex(w)
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
