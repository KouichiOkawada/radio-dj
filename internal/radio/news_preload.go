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
	ready   chan preparedNews
	status  *status.Server
	queue   *news.Queue
	store   *news.Store
	clock   *news.ProgramClock
	enabled bool
}

type preparedNews struct {
	kind     news.ProgramKind
	segments []Segment
}

const newsStockSize = 1

func startNewsPreloader(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, store *news.Store, clock *news.ProgramClock, st *status.Server) *newsPreloader {
	p := &newsPreloader{
		ready:   make(chan preparedNews, newsStockSize),
		status:  st,
		queue:   queue,
		store:   store,
		clock:   clock,
		enabled: vox != nil && queue != nil && store != nil && clock != nil && len(cfg.NewsFeeds) > 0,
	}
	if !p.enabled {
		st.SetNewsReadiness(false, 0, "unavailable")
		return p
	}

	st.SetNewsReadiness(false, 0, "loading")
	go p.run(cfg, djx, vox, queue, store, clock)
	return p
}

func (p *newsPreloader) publish(state string) {
	if p == nil || p.status == nil {
		return
	}
	count := len(p.ready)
	p.status.SetNewsReadiness(count > 0, count, state)
}

func (p *newsPreloader) run(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, store *news.Store, clock *news.ProgramClock) {
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
		slot := clock.Next(time.Now())
		items := queue.ReserveBulletin(store.Snapshot(), slot.Kind)
		if len(items) == 0 {
			// Quiet feeds are normal. Keep music on air and poll again later.
			p.publish("waiting")
			time.Sleep(30 * time.Second)
			continue
		}

		for i := range items {
			news.ResolveImage(&items[i])
		}
		bulletin := strings.TrimSpace(news.Script(items, cfg.Language))
		if bulletin == "" {
			log.Printf("[news] preload skipped empty bulletin")
			for _, item := range items {
				queue.Release(item)
			}
			time.Sleep(2 * time.Second)
			continue
		}

		speechPath, err := vox.Speak(bulletin)
		if err != nil {
			log.Printf("[news] preload TTS: %v", err)
			for _, item := range items {
				queue.Release(item)
			}
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

		segments := []Segment{{Path: mixedPath, IsNews: true, News: &items[0], NewsItems: items, Text: bulletin}}

		// The commentary is opinion/personality only. It is grounded on the RSS
		// source/title/description and is prepared before the break is advertised
		// READY, so a slow local reasoning model cannot create dead air later.
		if djx != nil {
			comment := strings.TrimSpace(djx.NewsComment(items[0].Source, items[0].Title, items[0].Description))
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

		p.ready <- preparedNews{kind: slot.Kind, segments: segments}
		log.Printf("[news] READY %s %d/%d: %s", slot.Kind, len(p.ready), cap(p.ready), items[0].Title)
		p.publish("ready")
	}
}

// markAired persists the story only when its factual news segment actually
// reaches the output. Rendering and queueing alone must not consume it.
func (p *newsPreloader) markAired(seg Segment) {
	if p == nil || p.queue == nil || seg.News == nil {
		return
	}
	if len(seg.NewsItems) > 0 {
		for _, item := range seg.NewsItems {
			p.queue.MarkAired(item)
		}
	} else {
		p.queue.MarkAired(*seg.News)
	}
}

// releaseSegments returns any reserved story from a discarded prefetched
// batch to the candidate pool, for example after an immediate mode switch.
func (p *newsPreloader) releaseSegments(segments []Segment) {
	if p == nil || p.queue == nil {
		return
	}
	for _, seg := range segments {
		if seg.IsNews && seg.News != nil {
			if len(seg.NewsItems) > 0 {
				for _, item := range seg.NewsItems {
					p.queue.Release(item)
				}
			} else {
				p.queue.Release(*seg.News)
			}
		}
	}
}

// tryTake is intentionally non-blocking. Radio mode simply carries on with
// music when no rendered bulletin is ready yet; news-only mode uses its normal
// music fallback while the preloader catches up.
func (p *newsPreloader) tryTake(kind news.ProgramKind) ([]Segment, bool) {
	if p == nil || !p.enabled {
		return nil, false
	}
	select {
	case prepared := <-p.ready:
		if kind != "" && prepared.kind != kind {
			// This can only happen after a mode switch or a just-expired slot.
			// Return it to the candidate pool rather than playing the wrong format.
			p.releaseSegments(prepared.segments)
			p.publish("loading")
			return nil, false
		}
		if len(p.ready) > 0 {
			p.publish("ready")
		} else {
			p.publish("loading")
		}
		return prepared.segments, true
	default:
		p.publish("loading")
		return nil, false
	}
}
