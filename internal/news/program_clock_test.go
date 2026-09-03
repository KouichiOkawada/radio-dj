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
