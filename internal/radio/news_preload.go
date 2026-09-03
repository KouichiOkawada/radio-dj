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
	queue   *news.Queue
	store   *news.Store
	enabled bool
}

const newsStockSize = 4

func startNewsPreloader(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, store *news.Store, st *status.Server) *newsPreloader {
	p := &newsPreloader{
		ready:   make(chan []Segment, newsStockSize),
		status:  st,
		queue:   queue,
		store:   store,
		enabled: vox != nil && queue != nil && store != nil && len(cfg.NewsFeeds) > 0,
	}
	if !p.enabled {
		st.SetNewsReadiness(false, 0, "unavailable")
		return p
	}

	st.SetNewsReadiness(false, 0, "loading")
	go p.run(cfg, djx, vox, queue, store)
	return p
}

func (p *newsPreloader) publish(state string) {
	if p == nil || p.status == nil {
		return
	}
	count := len(p.ready)
	p.status.SetNewsReadiness(count > 0, count, state)
}

func (p *newsPreloader) run(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, store *news.Store) {
	for {
		// Keep several complete breaks ready while music, news, or DJ commentary
		// is on air. Four items cover normal local LLM/TTS latency without letting
		// temporary audio or old headlines accumulate without bound.
		if len(p.ready) >= cap(p.ready) {
			p.publish("ready")
			time.Sleep(2 * time.Second)
			continue
		}

		p.publish("loading")
		item, ok := queue.ReserveItems(store.Snapshot(), time.Duration(cfg.NewsMaxAgeHours)*time.Hour)
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
			queue.Release(item)
			time.Sleep(2 * time.Second)
			continue
		}

		speechPath, err := vox.Speak(bulletin)
		if err != nil {
			log.Printf("[news] preload TTS: %v", err)
			queue.Release(item)
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
				// Keep the same bed underneath the DJ reaction so the whole news
				// break sounds continuous instead of dropping to dry narration.
				commentMixed, merr := news.MixWithBed("", commentPath, cfg.NewsBGMPath, filepath.Join(cfg.StateDir, "news"), cfg.Audio.NewsBGMVolume)
				if merr != nil {
					log.Printf("[news] preload DJ mix: %v", merr)
					commentMixed = commentPath
				}
				segments = append(segments, Segment{Path: commentMixed, IsDJ: true, Text: comment})
			} else {
				log.Printf("[news] preload DJ voice: %v", cerr)
			}
		}

		p.ready <- segments
		log.Printf("[news] READY %d/%d: %s", len(p.ready), cap(p.ready), item.Title)
		p.publish("ready")
	}
}

// markAired persists the story only when its factual news segment actually
// reaches the output. Rendering and queueing alone must not consume it.
func (p *newsPreloader) markAired(seg Segment) {
	if p == nil || p.queue == nil || seg.News == nil {
		return
	}
	p.queue.MarkAired(*seg.News)
}

// releaseSegments returns any reserved story from a discarded prefetched
// batch to the candidate pool, for example after an immediate mode switch.
func (p *newsPreloader) releaseSegments(segments []Segment) {
	if p == nil || p.queue == nil {
		return
	}
	for _, seg := range segments {
		if seg.IsNews && seg.News != nil {
			p.queue.Release(*seg.News)
		}
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
