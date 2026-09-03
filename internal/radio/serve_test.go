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

func TestNewsPreloaderWakeCancelsPollingDelay(t *testing.T) {
	p := &newsPreloader{enabled: true, wake: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() {
		p.wait(5 * time.Second)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	p.Wake()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Wake did not cancel the preloader polling delay")
	}
}
