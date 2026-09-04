package radio

import (
	"testing"
	"time"

	"radio-dj/internal/news"
	"radio-dj/internal/status"
)

func testNewsPreloader(t *testing.T) *newsPreloader {
	t.Helper()
	return &newsPreloader{
		ready:     make(chan preparedNews, 4),
		enabled:   true,
		status:    status.New(t.TempDir(), false, "/stream.mp3"),
		queue:     news.NewQueue(t.TempDir()),
		scheduled: map[string]bool{},
	}
}

func TestTryTakeFindsMatchingSlotBehindDifferentSlot(t *testing.T) {
	p := testNewsPreloader(t)
	first := news.ProgramSlot{Kind: news.ProgramFlash, At: time.Now().Add(time.Hour).Truncate(time.Second)}
	want := news.ProgramSlot{Kind: news.ProgramFull, At: first.At.Add(time.Hour)}
	p.ready <- preparedNews{slot: first, kind: first.Kind, scheduled: true, segments: []Segment{{IsNews: true, Text: "first"}}}
	p.ready <- preparedNews{slot: want, kind: want.Kind, scheduled: true, segments: []Segment{{IsNews: true, Text: "wanted"}}}
	p.readyStories.Store(2)

	segments, ok := p.tryTake(&want)
	if !ok || len(segments) != 1 || segments[0].Text != "wanted" {
		t.Fatalf("tryTake() = %#v, %v", segments, ok)
	}
	if len(p.ready) != 1 {
		t.Fatalf("nonmatching slot was lost; ready=%d", len(p.ready))
	}
}

func TestTryTakeDiscardsExpiredPreparedNews(t *testing.T) {
	p := testNewsPreloader(t)
	p.ready <- preparedNews{expiresAt: time.Now().Add(-time.Second), segments: []Segment{{IsNews: true, Text: "expired"}}}
	p.readyStories.Store(1)
	if segments, ok := p.tryTake(nil); ok || segments != nil {
		t.Fatalf("expired entry returned: %#v", segments)
	}
	if got := p.readyStories.Load(); got != 0 {
		t.Fatalf("readyStories=%d, want 0", got)
	}
}

func TestScheduledSlotCannotBePreparedTwice(t *testing.T) {
	p := testNewsPreloader(t)
	slot := news.ProgramSlot{Kind: news.ProgramFull, At: time.Now().Add(time.Hour).Truncate(time.Second)}
	p.scheduled[newsSlotKey(slot)] = true
	if p.canPrepareScheduled(slot) {
		t.Fatal("duplicate scheduled slot was accepted")
	}
}

func TestContinuousModeDoesNotConsumeScheduledBulletin(t *testing.T) {
	p := testNewsPreloader(t)
	slot := news.ProgramSlot{Kind: news.ProgramFlash, At: time.Now().Add(time.Hour).Truncate(time.Second)}
	p.ready <- preparedNews{slot: slot, kind: slot.Kind, scheduled: true, segments: []Segment{{IsNews: true, Text: "scheduled"}}}
	p.readyStories.Store(1)
	segments, ok := p.tryTake(nil)
	if ok || segments != nil {
		t.Fatalf("continuous mode consumed scheduled bulletin: %#v", segments)
	}
	if len(p.ready) != 1 {
		t.Fatal("scheduled bulletin was not preserved")
	}
}
