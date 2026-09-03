package news

import (
	"testing"
	"time"
)

func jst(y int, m time.Month, d, h, min int) time.Time {
	loc, _ := time.LoadLocation("Asia/Tokyo")
	return time.Date(y, m, d, h, min, 0, 0, loc)
}

func TestProgramClockDue(t *testing.T) {
	c := NewProgramClock()
	for _, tc := range []struct {
		at   time.Time
		kind ProgramKind
		ok   bool
	}{
		{jst(2026, time.September, 3, 20, 52), "", false},
		{jst(2026, time.September, 3, 21, 3), ProgramFull, true},
		{jst(2026, time.September, 3, 21, 11), "", false},
		{jst(2026, time.September, 3, 21, 30), ProgramFlash, true},
		{jst(2026, time.September, 3, 2, 30), "", false},
	} {
		slot, ok := c.Due(tc.at)
		if ok != tc.ok || (ok && slot.Kind != tc.kind) {
			t.Fatalf("Due(%s) = %#v, %v; want %s, %v", tc.at, slot, ok, tc.kind, tc.ok)
		}
		if ok {
			c.MarkAired(slot)
		}
	}
}

func TestProgramClockNextIncludesMarketSpecials(t *testing.T) {
	for _, tc := range []struct {
		at   time.Time
		kind ProgramKind
		hour int
		min  int
	}{
		{jst(2026, time.September, 3, 8, 44), ProgramMorningMarket, 8, 45},
		{jst(2026, time.September, 3, 8, 46), ProgramFull, 9, 0},
		{jst(2026, time.September, 3, 15, 39), ProgramTokyoClose, 15, 40},
		{jst(2026, time.September, 3, 23, 31), ProgramFull, 0, 0},
	} {
		slot := NewProgramClock().Next(tc.at)
		if slot.Kind != tc.kind || slot.At.Hour() != tc.hour || slot.At.Minute() != tc.min {
			t.Fatalf("Next(%s) = %#v; want %s at %02d:%02d", tc.at, slot, tc.kind, tc.hour, tc.min)
		}
	}
}
