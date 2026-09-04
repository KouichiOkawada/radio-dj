package news

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// Store is the in-memory candidate side of the news engine.  Collectors update
// it independently of programme preparation; playback and TTS therefore never
// make HTTP requests at a song boundary.
type Store struct {
	mu           sync.RWMutex
	items        map[string]Item
	excludeTerms []string
	imageSem     chan struct{}
	imagePending map[string]bool
}

func NewStore(excludeTerms ...string) *Store {
	clean := make([]string, 0, len(excludeTerms))
	for _, term := range excludeTerms {
		if term = strings.TrimSpace(term); term != "" {
			clean = append(clean, strings.ToLower(term))
		}
	}
	return &Store{items: map[string]Item{}, excludeTerms: clean, imageSem: make(chan struct{}, 3), imagePending: map[string]bool{}}
}

func (s *Store) Update(items []Item) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		if item.FetchedAt.IsZero() {
			item.FetchedAt = time.Now()
		}
		haystack := strings.ToLower(item.Title + " " + item.Description)
		excluded := false
		for _, term := range s.excludeTerms {
			if strings.Contains(haystack, term) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		key := itemKey(item)
		if key == "\n" {
			continue
		}
		s.items[key] = item
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	for key, item := range s.items {
		published, hasPublished := parsePublished(item.PublishedAt)
		if (hasPublished && published.Before(cutoff)) || (!hasPublished && item.FetchedAt.Before(cutoff)) {
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
	deduped := make([]Item, 0, len(out))
	seenTitles := map[string]bool{}
	for _, item := range out {
		titleKey := normalizeTitle(item.Title)
		if titleKey != "" && seenTitles[titleKey] {
			continue
		}
		seenTitles[titleKey] = true
		deduped = append(deduped, item)
	}
	return deduped
}

// ResolveImagesAsync enriches cards independently from collection, TTS and
// playback. Slow or broken article pages can never delay a ready bulletin.
func (s *Store) ResolveImagesAsync(items []Item) {
	if s == nil {
		return
	}
	queued := 0
	for _, candidate := range items {
		if candidate.ImageURL != "" || candidate.URL == "" {
			continue
		}
		key := itemKey(candidate)
		s.mu.Lock()
		if s.imagePending[key] {
			s.mu.Unlock()
			continue
		}
		s.imagePending[key] = true
		s.mu.Unlock()
		item := candidate
		go func() {
			s.imageSem <- struct{}{}
			defer func() { <-s.imageSem }()
			ResolveImage(&item)
			s.mu.Lock()
			key := itemKey(item)
			delete(s.imagePending, key)
			if current, ok := s.items[key]; ok && item.ImageURL != "" {
				current.ImageURL = item.ImageURL
				s.items[key] = current
			}
			s.mu.Unlock()
		}()
		queued++
		if queued == 20 {
			break
		}
	}
}

// StartCollectors runs independent, jittered RSS jobs. A broken publisher is
// isolated to its own goroutine and never delays a different source or audio.
func StartCollectors(ctx context.Context, store *Store, feeds []Feed) {
	for _, feed := range feeds {
		feed := feed
		go func() {
			interval := feedInterval(feed)
			fetch := func() {
				items := Fetch([]Feed{feed})
				store.Update(items)
				store.ResolveImagesAsync(items)
			}
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
	case "stock", "finance", "general":
		return 10 * time.Minute
	case "tech", "hokkaido":
		return 15 * time.Minute
	default:
		return 15 * time.Minute
	}
}
