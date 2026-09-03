package news

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// Store is the in-memory candidate side of the news engine.  Collectors update
// it independently of programme preparation; playback and TTS therefore never
// make HTTP requests at a song boundary.
type Store struct {
	mu    sync.RWMutex
	items map[string]Item
}

func NewStore() *Store { return &Store{items: map[string]Item{}} }

func (s *Store) Update(items []Item) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		key := itemKey(item)
		if key == "\n" {
			continue
		}
		s.items[key] = item
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	for key, item := range s.items {
		if published, ok := parsePublished(item.PublishedAt); ok && published.Before(cutoff) {
			delete(s.items, key)
		}
	}
}

func (s *Store) Snapshot() []Item {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	out := make([]Item, 0, len(s.items))
	for _, item := range s.items {
		out = append(out, item)
	}
	s.mu.RUnlock()
	sortItemsNewest(out)
	return out
}

// StartCollectors runs independent, jittered RSS jobs. A broken publisher is
// isolated to its own goroutine and never delays a different source or audio.
func StartCollectors(ctx context.Context, store *Store, feeds []Feed) {
	for _, feed := range feeds {
		feed := feed
		go func() {
			interval := feedInterval(feed)
			fetch := func() { store.Update(Fetch([]Feed{feed})) }
			fetch() // seed immediately, but this goroutine is never on the audio path
			jitter := time.Duration(rand.Int63n(int64(interval / 10)))
			timer := time.NewTimer(interval + jitter)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					fetch()
					timer.Reset(interval)
				}
			}
		}()
	}
}

func feedInterval(feed Feed) time.Duration {
	switch feed.Category {
	case "finance", "general":
		return 10 * time.Minute
	case "tech", "hokkaido":
		return 15 * time.Minute
	default:
		return 15 * time.Minute
	}
}
