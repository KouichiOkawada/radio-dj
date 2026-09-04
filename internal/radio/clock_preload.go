package radio

import (
	"fmt"
	"log"
	"sync"
	"time"

	"radio-dj/internal/voice"
)

// clockPreloader keeps the top-of-hour identification off the playback path.
// It prepares one truthful phrase per clock hour; the consumer inserts it only
// before the first spoken segment in that hour, never in the middle of music.
type clockPreloader struct {
	vox       *voice.Voice
	mu        sync.Mutex
	prepared  preparedClock
	preparing string
}

type preparedClock struct {
	hour string
	text string
	path string
}

func startClockPreloader(vox *voice.Voice) *clockPreloader {
	p := &clockPreloader{vox: vox}
	if vox == nil {
		return p
	}
	p.ensure(time.Now())
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for now := range ticker.C {
			p.ensure(now)
		}
	}()
	return p
}

func clockHourKey(now time.Time) string { return now.Format("2006-01-02T15") }

func clockAnnouncement(now time.Time) string {
	hour := now.Hour()
	if hour == 0 {
		return "日付が変わって、現在時刻は午前0時です。引き続きお付き合いください。"
	}
	if hour == 12 {
		return "正午を回りました。現在時刻は12時です。引き続きお付き合いください。"
	}
	return fmt.Sprintf("現在時刻は%d時を回りました。引き続きお付き合いください。", hour)
}

func (p *clockPreloader) ensure(now time.Time) {
	if p == nil || p.vox == nil {
		return
	}
	key := clockHourKey(now)
	p.mu.Lock()
	if p.prepared.hour == key || p.preparing == key {
		p.mu.Unlock()
		return
	}
	p.preparing = key
	p.mu.Unlock()
	go func() {
		text := clockAnnouncement(now)
		path, err := p.vox.Speak(text)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.preparing = ""
		if err != nil {
			log.Printf("[clock] preload voice: %v", err)
			return
		}
		if clockHourKey(time.Now()) != key {
			cleanupDJVoice(path)
			return
		}
		cleanupDJVoice(p.prepared.path)
		p.prepared = preparedClock{hour: key, text: text, path: path}
		log.Printf("[clock] READY %s", key)
	}()
}

func (p *clockPreloader) TryTake(now time.Time, alreadyAnnounced string) (preparedClock, bool) {
	key := clockHourKey(now)
	if p == nil || key == alreadyAnnounced {
		return preparedClock{}, false
	}
	p.ensure(now)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prepared.hour != key || p.prepared.path == "" {
		return preparedClock{}, false
	}
	ready := p.prepared
	p.prepared = preparedClock{}
	return ready, true
}
