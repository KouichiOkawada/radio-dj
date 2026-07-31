// Package library picks the next track to play. Two sources, same interface:
// "folder" walks a local dir (works with zero config), "navidrome" pulls over
// the Subsonic API (needs RDJ_NAVIDROME_USER/PASS). Both hand back a track
// whose Src is anything `ffmpeg -i` accepts — a file path or an http URL.
package library

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Track struct {
	Src    string `json:"src"`    // file path or http URL — fed to ffmpeg -i
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album,omitempty"`
	Year   string `json:"year,omitempty"`
	BPM    string `json:"bpm,omitempty"`
}

type Library interface {
	Next() (Track, error)
	// Sample returns up to n not-yet-played tracks WITHOUT committing them.
	// Used to offer the LLM director a shortlist; MarkPlayed commits the chosen.
	Sample(n int) []Track
	// MarkPlayed commits a track (by Src) as played.
	MarkPlayed(src string)
	// Search returns tracks whose title/artist/album contains q (case-insensitive).
	Search(q string) ([]Track, error)
}

func New(source, folder, ndURL, ndUser, ndPass string) (Library, error) {
	switch source {
	case "navidrome":
		if ndUser == "" || ndPass == "" {
			return nil, fmt.Errorf("navidrome source needs RDJ_NAVIDROME_USER and RDJ_NAVIDROME_PASS")
		}
		return &navidrome{base: ndURL, user: ndUser, pass: ndPass}, nil
	default:
		return newFolder(folder)
	}
}

// ---------- folder ----------

type folder struct {
	root   string
	files  []string
	played map[string]bool
	mu     sync.Mutex
	cursor int
}

func newFolder(root string) (*folder, error) {
	var files []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if isAudio(p) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no audio files under %s", root)
	}
	rand.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })
	return &folder{root: root, files: files, played: map[string]bool{}}, nil
}

func (f *folder) Next() (Track, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.played) >= len(f.files) {
		f.played = map[string]bool{} // every track played once → reset, reshuffle
		rand.Shuffle(len(f.files), func(i, j int) { files := f.files; files[i], files[j] = files[j], files[i] })
	}
	for n := 0; n < len(f.files); n++ {
		p := f.files[f.cursor%len(f.files)]
		f.cursor++
		if f.played[p] {
			continue
		}
		f.played[p] = true
		m := probe(p)
		return Track{
			Src: p,
			Title:  fallback(m.Title, stripExt(filepath.Base(p))),
			Artist: fallback(m.Artist, parentDir(p, f.root)),
			Album:  m.Album,
			Year:   m.Year,
			BPM:    m.BPM,
		}, nil
	}
	// shouldn't reach here, but stay alive
	f.played = map[string]bool{}
	return f.Next()
}

// Sample peeks up to n unplayed tracks without committing them. The director
// picks a subset; the caller MarkPlayed only those. Scans the shuffled order
// and resets+reshuffles once every track has played.
func (f *folder) Sample(n int) []Track {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.played) >= len(f.files) {
		f.played = map[string]bool{}
		rand.Shuffle(len(f.files), func(i, j int) { f.files[i], f.files[j] = f.files[j], f.files[i] })
	}
	var out []Track
	for _, p := range f.files {
		if f.played[p] {
			continue
		}
		m := probe(p)
		out = append(out, Track{
			Src:    p,
			Title:  fallback(m.Title, stripExt(filepath.Base(p))),
			Artist: fallback(m.Artist, parentDir(p, f.root)),
			Album:  m.Album,
			Year:   m.Year,
			BPM:    m.BPM,
		})
		if len(out) >= n {
			break
		}
	}
	return out
}

func (f *folder) MarkPlayed(src string) {
	f.mu.Lock()
	f.played[src] = true
	f.mu.Unlock()
}

func (f *folder) Search(q string) ([]Track, error) {
	q = strings.ToLower(q)
	if q == "" {
		return nil, nil
	}
	var out []Track
	for _, p := range f.files {
		hay := strings.ToLower(filepath.Base(p) + " " + parentDir(p, f.root))
		if strings.Contains(hay, q) {
			m := probe(p)
			out = append(out, Track{
				Src:    p,
				Title:  fallback(m.Title, stripExt(filepath.Base(p))),
				Artist: fallback(m.Artist, parentDir(p, f.root)),
				Album:  m.Album,
				Year:   m.Year,
				BPM:    m.BPM,
			})
			if len(out) >= 10 {
				break
			}
		}
	}
	return out, nil
}

// ---------- navidrome (Subsonic) ----------

type navidrome struct {
	base, user, pass string
	queue            []Track
}

func (n *navidrome) Next() (Track, error) {
	if len(n.queue) == 0 {
		if err := n.refill(); err != nil {
			return Track{}, err
		}
	}
	t := n.queue[0]
	n.queue = n.queue[1:]
	return t, nil
}

// Sample drains the random-song queue up to n tracks without a separate commit
// step — Navidrome has no global played-set, so sampling consumes the queue.
func (n *navidrome) Sample(nn int) []Track {
	var out []Track
	for len(out) < nn {
		if len(n.queue) == 0 {
			if err := n.refill(); err != nil {
				break
			}
		}
		take := nn - len(out)
		if take > len(n.queue) {
			take = len(n.queue)
		}
		out = append(out, n.queue[:take]...)
		n.queue = n.queue[take:]
	}
	return out
}

// MarkPlayed is a no-op for Navidrome (no persistent played-set).
func (n *navidrome) MarkPlayed(src string) {}

func (n *navidrome) Search(q string) ([]Track, error) {
	v := url.Values{}
	v.Set("u", n.user)
	v.Set("p", n.pass)
	v.Set("v", "1.16.1")
	v.Set("c", "radio-dj")
	v.Set("f", "json")
	v.Set("query", q)
	v.Set("count", "10")
	endpoint := strings.TrimRight(n.base, "/") + "/rest/search3.view?" + v.Encode()
	resp, err := http.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var doc struct {
		SubsonicResponse struct {
			SearchResult3 struct {
				Song []struct {
					ID, Title, Artist, Album string
					Year                    int
				} `json:"song"`
			} `json:"searchResult3"`
		} `json:"subsonic-response"`
	}
	_ = json.Unmarshal(body, &doc)
	var out []Track
	for _, s := range doc.SubsonicResponse.SearchResult3.Song {
		sv := url.Values{}
		sv.Set("u", n.user)
		sv.Set("p", n.pass)
		sv.Set("v", "1.16.1")
		sv.Set("c", "radio-dj")
		sv.Set("id", s.ID)
		out = append(out, Track{
			Src:    strings.TrimRight(n.base, "/") + "/rest/stream.view?" + sv.Encode(),
			Title:  s.Title,
			Artist: s.Artist,
			Album:  s.Album,
			Year:   strconv.Itoa(s.Year),
		})
	}
	return out, nil
}

func (n *navidrome) refill() error {
	// Plain-password auth (p=). Works on default Navidrome; switch to salted
	// token (t/s) if the operator disabled legacy auth.
	v := url.Values{}
	v.Set("u", n.user)
	v.Set("p", n.pass)
	v.Set("v", "1.16.1")
	v.Set("c", "radio-dj")
	v.Set("f", "json")
	v.Set("size", "50")
	endpoint := strings.TrimRight(n.base, "/") + "/rest/getRandomSongs.view?" + v.Encode()
	resp, err := http.Get(endpoint)
	if err != nil {
		return fmt.Errorf("navidrome getRandomSongs: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var doc struct {
		SubsonicResponse struct {
			RandomSongs struct {
				Song []struct {
					ID, Title, Artist, Album, Suffix string
					Year                             int
				} `json:"song"`
			} `json:"randomSongs"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("navidrome parse: %w", err)
	}
	if doc.SubsonicResponse.RandomSongs.Song == nil {
		return fmt.Errorf("navidrome: empty response (%s)", strings.TrimSpace(string(body)))
	}
	for _, s := range doc.SubsonicResponse.RandomSongs.Song {
		sv := url.Values{}
		sv.Set("u", n.user)
		sv.Set("p", n.pass)
		sv.Set("v", "1.16.1")
		sv.Set("c", "radio-dj")
		sv.Set("id", s.ID)
		streamURL := strings.TrimRight(n.base, "/") + "/rest/stream.view?" + sv.Encode()
		n.queue = append(n.queue, Track{
			Src:    streamURL,
			Title:  s.Title,
			Artist: s.Artist,
			Album:  s.Album,
			Year:   strconv.Itoa(s.Year),
		})
	}
	return nil
}

// ---------- helpers ----------

var audioExts = map[string]bool{".mp3": true, ".m4a": true, ".flac": true, ".wav": true, ".ogg": true, ".opus": true}

func isAudio(p string) bool { return audioExts[strings.ToLower(filepath.Ext(p))] }

func stripExt(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

func parentDir(p, root string) string {
	rel, err := filepath.Rel(root, filepath.Dir(p))
	if err != nil || rel == "." {
		return ""
	}
	return rel
}

// Duration returns a track's playback length via ffprobe (cached per path).
// Falls back to 3min if ffprobe fails so the loop never blocks on metadata.
func Duration(path string) time.Duration {
	return probe(path).Duration
}

// fileMeta bundles everything probe() extracts in one ffprobe call.
type fileMeta struct {
	Duration time.Duration
	Title    string
	Artist   string
	Album    string
	Year     string
	BPM      string
}

var probeCache sync.Map // path → fileMeta

// probe runs ffprobe once per file (cached) to extract duration + ID3 tags.
// Streaming URLs (http://) skip probing — returns duration 3min, empty tags.
func probe(path string) fileMeta {
	if v, ok := probeCache.Load(path); ok {
		return v.(fileMeta)
	}
	m := fileMeta{Duration: 180 * time.Second}
	if !strings.Contains(path, "://") {
		out, err := exec.Command("ffprobe", "-v", "error",
			"-show_entries", "format=duration:format_tags=title,artist,album,date,TBPM",
			"-of", "json", path).Output()
		if err == nil {
			var doc struct {
				Format struct {
					Duration string `json:"duration"`
					Tags     struct {
						Title, Artist, Album, Date, TBPM string
					} `json:"tags"`
				} `json:"format"`
			}
			if json.Unmarshal(out, &doc) == nil {
				if f, err := strconv.ParseFloat(strings.TrimSpace(doc.Format.Duration), 64); err == nil && f > 0 {
					m.Duration = time.Duration(f * float64(time.Second))
				}
				m.Title = doc.Format.Tags.Title
				m.Artist = doc.Format.Tags.Artist
				m.Album = cleanAlbum(doc.Format.Tags.Album)
				m.Year = yearOf(doc.Format.Tags.Date)
				m.BPM = strings.TrimSpace(doc.Format.Tags.TBPM)
			}
		}
	}
	probeCache.Store(path, m)
	return m
}

// fallback returns a if non-empty, else b.
func fallback(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// cleanAlbum treats placeholder names as empty.
func cleanAlbum(s string) string {
	s = strings.TrimSpace(s)
	if s == "- Unknown Album" || s == "Unknown Album" {
		return ""
	}
	return s
}

// yearOf extracts the 4-digit year from a date tag (handles YYYYMMDD, YYYY).
func yearOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 4 {
		return s[:4]
	}
	return s
}

