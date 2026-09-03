package radio

import (
	"testing"
	"time"

	"radio-dj/internal/news"
)

func TestMayInterruptForNewsOnlyInContinuousOrDueRadioSlot(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tokyo")
	at := func(hour, minute int) time.Time {
		return time.Date(2026, time.September, 4, hour, minute, 0, 0, loc)
	}
	if mayInterruptForNews("radio", news.NewProgramClock(), at(6, 15)) {
		t.Fatal("radio music was interruptible before a scheduled news slot")
	}
	if !mayInterruptForNews("radio", news.NewProgramClock(), at(6, 30)) {
		t.Fatal("radio did not allow interruption at a scheduled news slot")
	}
	if !mayInterruptForNews("news", news.NewProgramClock(), at(6, 15)) {
		t.Fatal("news continuous should replace filler as soon as news is ready")
	}
	if mayInterruptForNews("music", news.NewProgramClock(), at(6, 30)) {
		t.Fatal("music-only mode must never be interrupted by news")
	}
}
