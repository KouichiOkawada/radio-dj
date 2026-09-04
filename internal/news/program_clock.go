package news

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProgramKind identifies an editorial slot.  The clock intentionally knows
// nothing about fetching, TTS or playback: it only says which ready bulletin is
// eligible at a natural song boundary.
type ProgramKind string

const (
	ProgramFull          ProgramKind = "full"
	ProgramFlash         ProgramKind = "flash"
	ProgramMorningMarket ProgramKind = "morning_market"
	ProgramTokyoClose    ProgramKind = "tokyo_close"
)

type ProgramSlot struct {
	Kind ProgramKind
	At   time.Time
}

// ProgramClock is a testable Asia/Tokyo programme clock. A slot stays eligible
// for a short grace period so music is never cut at the exact wall-clock time.
// Once consumed it cannot be emitted twice in the same process.
type ProgramClock struct {
	loc      *time.Location
	consumed map[string]bool
	path     string
	mu       sync.Mutex
}

func NewProgramClock(stateDir ...string) *ProgramClock {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		loc = time.FixedZone("JST", 9*60*60)
	}
	c := &ProgramClock{loc: loc, consumed: map[string]bool{}}
	if len(stateDir) > 0 && stateDir[0] != "" {
		c.path = filepath.Join(stateDir[0], "program-slots.json")
		if data, err := os.ReadFile(c.path); err == nil {
			_ = json.Unmarshal(data, &c.consumed)
		}
	}
	return c
}

func (c *ProgramClock) slotKey(slot ProgramSlot) string {
	return string(slot.Kind) + ":" + slot.At.Format(time.RFC3339)
}

// Due returns the most recent eligible scheduled item. Full news runs on the
// hour all day; flash runs on the half hour only from 06:00 through 23:59.
// Market specials intentionally supersede a regular item at their own time.
func (c *ProgramClock) Due(now time.Time) (ProgramSlot, bool) {
	if c == nil {
		return ProgramSlot{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now = now.In(c.loc)
	var candidates []ProgramSlot
	for _, minute := range []int{0, 30} {
		at := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), minute, 0, 0, c.loc)
		if at.After(now) {
			at = at.Add(-time.Hour)
		}
		if minute == 0 {
			candidates = append(candidates, ProgramSlot{Kind: ProgramFull, At: at})
		} else if at.Hour() >= 6 && at.Hour() <= 23 {
			candidates = append(candidates, ProgramSlot{Kind: ProgramFlash, At: at})
		}
	}
	for _, special := range []struct {
		kind ProgramKind
		hour int
		min  int
	}{{ProgramMorningMarket, 8, 45}, {ProgramTokyoClose, 15, 40}} {
		at := time.Date(now.Year(), now.Month(), now.Day(), special.hour, special.min, 0, 0, c.loc)
		if at.After(now) {
			at = at.AddDate(0, 0, -1)
		}
		candidates = append(candidates, ProgramSlot{Kind: special.kind, At: at})
	}
	// Regular slots may play after the current song, but never turn into stale
	// "news" twenty minutes later. Market segments get the same grace.
	const grace = 10 * time.Minute
	var due ProgramSlot
	for _, candidate := range candidates {
		if now.Sub(candidate.At) < 0 || now.Sub(candidate.At) > grace || c.consumed[c.slotKey(candidate)] {
			continue
		}
		if due.At.IsZero() || candidate.At.After(due.At) {
			due = candidate
		}
	}
	return due, !due.At.IsZero()
}

func (c *ProgramClock) MarkAired(slot ProgramSlot) {
	if c == nil || slot.At.IsZero() {
		return
	}
	c.mu.Lock()
	c.consumed[c.slotKey(slot)] = true
	if c.path != "" {
		data, _ := json.MarshalIndent(c.consumed, "", "  ")
		_ = os.WriteFile(c.path, data, 0o644)
	}
	c.mu.Unlock()
}

// Next reports the next scheduled programme for the player status UI.
func (c *ProgramClock) Next(now time.Time) ProgramSlot {
	if c == nil {
		return ProgramSlot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now = now.In(c.loc)
	for i := 0; i <= 24*60; i++ {
		candidate := now.Add(time.Duration(i) * time.Minute).Truncate(time.Minute)
		if candidate.Before(now) {
			continue
		}
		if candidate.Hour() == 8 && candidate.Minute() == 45 {
			slot := ProgramSlot{Kind: ProgramMorningMarket, At: candidate}
			if !c.consumed[c.slotKey(slot)] {
				return slot
			}
		}
		if candidate.Hour() == 15 && candidate.Minute() == 40 {
			slot := ProgramSlot{Kind: ProgramTokyoClose, At: candidate}
			if !c.consumed[c.slotKey(slot)] {
				return slot
			}
		}
		if candidate.Minute() == 0 || (candidate.Minute() == 30 && candidate.Hour() >= 6) {
			slot := ProgramSlot{Kind: map[bool]ProgramKind{true: ProgramFull, false: ProgramFlash}[candidate.Minute() == 0], At: candidate}
			if !c.consumed[c.slotKey(slot)] {
				return slot
			}
		}
	}
	return ProgramSlot{}
}
