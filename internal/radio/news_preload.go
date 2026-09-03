package radio

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	ready        chan preparedNews
	status       *status.Server
	queue        *news.Queue
	store        *news.Store
	clock        *news.ProgramClock
	enabled      bool
	mu           sync.RWMutex
	priority     string
	source       string
	sources      map[string]bool
	generation   atomic.Uint64
	readyStories atomic.Int32
}

type preparedNews struct {
	kind     news.ProgramKind
	slot     news.ProgramSlot
	segments []Segment
}

const newsStockSize = 2

func startNewsPreloader(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, store *news.Store, clock *news.ProgramClock, st *status.Server) *newsPreloader {
	p := &newsPreloader{
		ready:   make(chan preparedNews, newsStockSize),
		status:  st,
		queue:   queue,
		store:   store,
		clock:   clock,
		sources: map[string]bool{},
		enabled: vox != nil && queue != nil && store != nil && clock != nil && len(cfg.NewsFeeds) > 0,
	}
	for _, feed := range cfg.NewsFeeds {
		p.sources[feed.Name] = true
	}
	if !p.enabled {
		st.SetNewsReadiness(false, 0, "unavailable")
		return p
	}

	st.SetNewsReadiness(false, 0, "loading")
	p.pruneAudio(filepath.Join(cfg.StateDir, "news"), 24*time.Hour)
	go p.run(cfg, djx, vox, queue, store, clock)
	return p
}

func (p *newsPreloader) publish(state string) {
	if p == nil || p.status == nil {
		return
	}
	count := int(p.readyStories.Load())
	if len(p.ready) > 0 && state != "unavailable" {
		state = "ready"
	}
	p.status.SetNewsReadiness(count > 0, count, state)
}

func (p *newsPreloader) run(cfg config.Config, djx *dj.DJ, vox *voice.Voice, queue *news.Queue, store *news.Store, clock *news.ProgramClock) {
	market := news.NewJQuants(cfg.JQuantsAPIKey)
	for {
		// Keep several complete, article-sized breaks ready while music, news, or
		// DJ commentary is on air. Each entry has its own metadata, so the player
		// can switch its article card exactly when the next story begins.
		if len(p.ready) >= cap(p.ready) {
			p.publish("ready")
			time.Sleep(2 * time.Second)
			continue
		}

		p.publish("loading")
		slot := clock.Next(time.Now())
		p.status.SetNextNewsSlot(string(slot.Kind), slot.At)
		generation := p.generation.Load()
		snapshot := store.Snapshot()
		priority := p.Priority()
		source := p.Source()
		if source != "" {
			filtered := make([]news.Item, 0, len(snapshot))
			for _, item := range snapshot {
				if item.Source == source {
					filtered = append(filtered, item)
				}
			}
			snapshot = filtered
		}
		var items []news.Item
		if priority != "" {
			if item, ok := queue.ReserveItemsPreferred(snapshot, time.Duration(cfg.NewsMaxAgeHours)*time.Hour, priority); ok {
				items = []news.Item{item}
			}
		} else {
			items = queue.ReserveBulletin(snapshot, slot.Kind)
		}
		if len(items) == 0 {
			// Quiet feeds are normal. Keep music on air and poll again later.
			p.publish("waiting")
			time.Sleep(30 * time.Second)
			continue
		}

		for i := range items {
			news.ResolveImage(&items[i])
		}
		p.status.SetNewsPreview(toNewsStatus(items[0], ""))
		segments := make([]Segment, 0, len(items)+1)
		renderFailed := false
		for _, item := range items {
			bulletin := strings.TrimSpace(news.Script([]news.Item{item}, cfg.Language))
			if bulletin == "" {
				log.Printf("[news] preload skipped empty bulletin: %s", item.Title)
				renderFailed = true
				break
			}
			speechPath, err := vox.Speak(bulletin)
			if err != nil {
				log.Printf("[news] preload TTS: %v", err)
				renderFailed = true
				break
			}
			mixedPath, err := news.MixWithBed("", speechPath, cfg.NewsBGMPath, filepath.Join(cfg.StateDir, "news"), cfg.Audio.NewsBGMVolume)
			if err != nil {
				log.Printf("[news] preload mix: %v", err)
				mixedPath = speechPath
			} else if mixedPath != speechPath {
				log.Printf("[news] BGM mixed under bulletin at %.0f%%", cfg.Audio.NewsBGMVolume*100)
			}
			itemCopy := item
			segments = append(segments, Segment{Path: mixedPath, IsNews: true, News: &itemCopy, NewsItems: []news.Item{itemCopy}, Text: bulletin})
		}
		if renderFailed {
			p.releaseSegments(segments)
			for _, item := range items {
				queue.Release(item)
			}
			p.publish("loading")
			time.Sleep(5 * time.Second)
			continue
		}

		// The commentary is opinion/personality only. It is grounded on the RSS
		// source/title/description and is prepared before the break is advertised
		// READY, so a slow local reasoning model cannot create dead air later.
		if djx != nil && (slot.Kind != news.ProgramFlash || priority != "" || source != "") {
			item := items[0]
			for _, candidate := range items {
				if strings.EqualFold(candidate.Category, "stock") {
					item = candidate
					break
				}
			}
			marketContext := ""
			if strings.EqualFold(item.Category, "stock") {
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				marketContext = market.MarketContext(ctx, cfg.WatchSymbols)
				cancel()
				if marketContext != "" {
					log.Printf("[news] J-Quants context ready for %d watch symbol(s)", len(cfg.WatchSymbols))
				}
			}
			log.Printf("[news] AI context: RSS source=%q category=%s J-Quants=%t", item.Source, item.Category, marketContext != "")
			comment := strings.TrimSpace(djx.NewsBriefComment(item.Source, item.Title, item.Description, marketContext))
			if comment == "" {
				comment = "「" + item.Title + "」という動きが、これから私たちの暮らしや選択にどう響くのか。事実と今後の変化を分けながら、落ち着いて見ていきたいですね。"
			}
			if commentPath, cerr := vox.Speak(comment); cerr == nil {
				// Keep the same bed underneath the DJ reaction so the whole news
				// break sounds continuous instead of dropping to dry narration.
				commentMixed, merr := news.MixWithBed("", commentPath, cfg.NewsBGMPath, filepath.Join(cfg.StateDir, "news"), cfg.Audio.NewsBGMVolume)
				if merr != nil {
					log.Printf("[news] preload DJ mix: %v", merr)
					commentMixed = commentPath
				} else if commentMixed != commentPath {
					log.Printf("[news] BGM mixed under AI commentary at %.0f%%", cfg.Audio.NewsBGMVolume*100)
				}
				// Keep the related article on screen throughout the commentary instead
				// of replacing it with a generic AI DJ card.
				segments = append(segments, Segment{Path: commentMixed, IsDJ: true, News: &item, Text: comment})
			} else {
				log.Printf("[news] preload DJ voice: %v", cerr)
			}
		}

		if generation != p.generation.Load() {
			p.releaseSegments(segments)
			continue
		}
		p.readyStories.Add(int32(countNewsSegments(segments)))
		p.ready <- preparedNews{kind: slot.Kind, slot: slot, segments: segments}
		log.Printf("[news] READY %s %d story/stories in slot %d/%d: %s", slot.Kind, countNewsSegments(segments), len(p.ready), cap(p.ready), items[0].Title)
		p.publish("ready")
	}
}

func (p *newsPreloader) Priority() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.priority
}

func (p *newsPreloader) Source() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.source
}

func (p *newsPreloader) SetPriority(category string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	switch category {
	case "", "stock", "finance", "general", "hokkaido", "tech":
	default:
		return false
	}
	p.mu.Lock()
	p.priority = category
	p.mu.Unlock()
	p.invalidateReady()
	return true
}

func (p *newsPreloader) SetSource(source string) bool {
	source = strings.TrimSpace(source)
	if p == nil || (source != "" && !p.sources[source]) {
		return false
	}
	p.mu.Lock()
	p.source = source
	p.mu.Unlock()
	p.invalidateReady()
	return true
}

func (p *newsPreloader) invalidateReady() {
	p.generation.Add(1)
	for {
		select {
		case prepared := <-p.ready:
			p.readyStories.Add(-int32(countNewsSegments(prepared.segments)))
			p.releaseSegments(prepared.segments)
		default:
			p.publish("loading")
			return
		}
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
		p.cleanupSegment(seg)
	}
}

func (p *newsPreloader) cleanupSegment(seg Segment) {
	if p == nil || (!seg.IsNews && !seg.IsDJ) || seg.Path == "" {
		return
	}
	newsDir, err := filepath.Abs(filepath.Join(p.status.StateDir(), "news"))
	if err != nil {
		return
	}
	path, err := filepath.Abs(seg.Path)
	if err == nil && filepath.Dir(path) == newsDir {
		_ = os.Remove(path)
	}
}

func (p *newsPreloader) pruneAudio(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "news-") || !strings.HasSuffix(strings.ToLower(entry.Name()), ".mp3") {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// tryTake is intentionally non-blocking. Radio mode simply carries on with
// music when no rendered bulletin is ready yet; news-only mode uses its normal
// music fallback while the preloader catches up.
func (p *newsPreloader) tryTake(slot *news.ProgramSlot) ([]Segment, bool) {
	if p == nil || !p.enabled {
		return nil, false
	}
	select {
	case prepared := <-p.ready:
		p.readyStories.Add(-int32(countNewsSegments(prepared.segments)))
		if slot != nil && (prepared.kind != slot.Kind || !prepared.slot.At.Equal(slot.At)) {
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

func countNewsSegments(segments []Segment) int {
	count := 0
	for _, segment := range segments {
		if segment.IsNews {
			count++
		}
	}
	return count
}
