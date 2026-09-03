package radio

import (
	"log"
	"path/filepath"
	"strings"
	"time"

	"radio-dj/internal/config"
	"radio-dj/internal/dj"
	"radio-dj/internal/news"
	"radio-dj/internal/status"
	"radio-dj/internal/voice"
)

// newsPreloader renders complete news breaks away from the playback path.
// A ready entry already contains RSS metadata, deterministic bulletin text,
// TTS, optional news-bed mix, and (when available) the grounded AI-DJ reaction.
// Playback therefore never waits for RSS, Ollama, edge-tts, or ffmpeg.
type newsPreloader struct {
	ready   chan []Segment
	status  *status.Server
	enabled bool
}

func startNewsPreloader(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, st *status.Server) *newsPreloader {
	p := &newsPreloader{
		ready:   make(chan []Segment, 2),
		status:  st,
		enabled: vox != nil && queue != nil && len(cfg.NewsFeeds) > 0,
	}
	if !p.enabled {
		st.SetNewsReadiness(false, 0, "unavailable")
		return p
	}

	st.SetNewsReadiness(false, 0, "loading")
	go p.run(cfg, djx, vox, queue)
	return p
}

func (p *newsPreloader) publish(state string) {
	if p == nil || p.status == nil {
		return
	}
	count := len(p.ready)
	p.status.SetNewsReadiness(count > 0, count, state)
}

func (p *newsPreloader) run(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue) {
	for {
		// Two complete breaks are enough to hide generation latency without
		// allowing temporary MP3 files or already-selected stories to grow forever.
		if len(p.ready) >= cap(p.ready) {
			p.publish("ready")
			time.Sleep(2 * time.Second)
			continue
		}

		p.publish("loading")
		item, ok := queue.Next(toNewsFeeds(cfg.NewsFeeds), time.Duration(cfg.NewsMaxAgeHours)*time.Hour)
		if !ok {
			// Quiet feeds are normal. Keep music on air and poll again later.
			p.publish("waiting")
			time.Sleep(30 * time.Second)
			continue
		}

		news.ResolveImage(&item)
		bulletin := strings.TrimSpace(news.Script([]news.Item{item}, cfg.Language))
		if bulletin == "" {
			log.Printf("[news] preload skipped empty bulletin: %s", item.Title)
			time.Sleep(2 * time.Second)
			continue
		}

		speechPath, err := vox.Speak(bulletin)
		if err != nil {
			log.Printf("[news] preload TTS: %v", err)
			p.publish("loading")
			time.Sleep(5 * time.Second)
			continue
		}

		mixedPath, err := news.MixWithBed("", speechPath, cfg.NewsBGMPath, filepath.Join(cfg.StateDir, "news"), cfg.Audio.NewsBGMVolume)
		if err != nil {
			// MixWithBed is deliberately fail-soft, but retain this guard if a
			// future implementation returns a hard error for invalid speech input.
			log.Printf("[news] preload mix: %v", err)
			mixedPath = speechPath
		}

		segments := []Segment{{Path: mixedPath, IsNews: true, News: &item, Text: bulletin}}

		// The commentary is opinion/personality only. It is grounded on the RSS
		// source/title/description and is prepared before the break is advertised
		// READY, so a slow local reasoning model cannot create dead air later.
		if djx != nil {
			comment := strings.TrimSpace(djx.NewsComment(item.Source, item.Title, item.Description))
			if comment == "" {
				comment = "この話題、これからどう動くのか気になりますね。続報も確認していきたいところです。"
			}
			if commentPath, cerr := vox.Speak(comment); cerr == nil {
				segments = append(segments, Segment{Path: commentPath, IsDJ: true, Text: comment})
			} else {
				log.Printf("[news] preload DJ voice: %v", cerr)
			}
		}

		p.ready <- segments
		log.Printf("[news] READY %d/%d: %s", len(p.ready), cap(p.ready), item.Title)
		p.publish("ready")
	}
}

// tryTake is intentionally non-blocking. Radio mode simply carries on with
// music when no rendered bulletin is ready yet; news-only mode uses its normal
// music fallback while the preloader catches up.
func (p *newsPreloader) tryTake() ([]Segment, bool) {
	if p == nil || !p.enabled {
		return nil, false
	}
	select {
	case segments := <-p.ready:
		if len(p.ready) > 0 {
			p.publish("ready")
		} else {
			p.publish("loading")
		}
		return segments, true
	default:
		p.publish("loading")
		return nil, false
	}
}
