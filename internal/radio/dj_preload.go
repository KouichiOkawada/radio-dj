package radio

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"radio-dj/internal/dj"
	"radio-dj/internal/library"
	"radio-dj/internal/voice"
)

// djPreloader keeps LLM and TTS completely outside the audio path. A missed
// deadline means music continues; playback never waits for a talk result.
type djPreloader struct {
	jobs    chan library.Track
	ready   chan preparedDJ
	enabled bool
	mu      sync.RWMutex
	desired string
}

type preparedDJ struct {
	track library.Track
	path  string
	text  string
}

func startDJPreloader(host *dj.DJ, vox *voice.Voice) *djPreloader {
	p := &djPreloader{jobs: make(chan library.Track, 1), ready: make(chan preparedDJ, 1), enabled: host != nil && vox != nil}
	if !p.enabled {
		return p
	}
	go func() {
		for track := range p.jobs {
			text := strings.TrimSpace(host.Backsell(track.Title, track.Artist, track.Album))
			if text == "" {
				continue
			}
			path, err := vox.Speak(text)
			if err != nil {
				log.Printf("[dj] preload voice: %v", err)
				continue
			}
			p.mu.RLock()
			stillWanted := p.desired == track.Src
			p.mu.RUnlock()
			if !stillWanted {
				cleanupDJVoice(path)
				continue
			}
			prepared := preparedDJ{track: track, path: path, text: text}
			select {
			case p.ready <- prepared:
				log.Printf("[dj] READY after %s", track.Title)
			default:
				cleanupDJVoice(path)
			}
		}
	}()
	return p
}

func (p *djPreloader) Prepare(track library.Track) {
	if p == nil || !p.enabled || strings.TrimSpace(track.Src) == "" {
		return
	}
	p.mu.Lock()
	p.desired = track.Src
	p.mu.Unlock()
	select {
	case stale := <-p.ready:
		cleanupDJVoice(stale.path)
	default:
	}
	select {
	case stale := <-p.jobs:
		_ = stale // keep only the latest desired track when the worker is busy
	default:
	}
	select {
	case p.jobs <- track:
	default:
	}
}

func (p *djPreloader) TryTake(track library.Track) (preparedDJ, bool) {
	if p == nil || !p.enabled {
		return preparedDJ{}, false
	}
	select {
	case prepared := <-p.ready:
		if prepared.track.Src != track.Src {
			cleanupDJVoice(prepared.path)
			return preparedDJ{}, false
		}
		return prepared, true
	default:
		return preparedDJ{}, false
	}
}

func cleanupDJVoice(path string) {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "radio-dj-voice-") {
		_ = os.Remove(path)
	}
}
