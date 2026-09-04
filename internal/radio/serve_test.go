package radio

import (
	"testing"
	"time"
)

func TestPlaybackModeAppliesRequestOnlyAtBoundary(t *testing.T) {
	modes := newPlaybackMode("radio")
	current, pending := modes.Request("news")
	if current != "radio" || pending != "news" || modes.Get() != "radio" {
		t.Fatalf("request changed on-air mode: current=%q pending=%q on-air=%q", current, pending, modes.Get())
	}
	applied, ok := modes.ApplyPending()
	if !ok || applied != "news" || modes.Get() != "news" {
		t.Fatalf("boundary did not apply request: applied=%q ok=%v", applied, ok)
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
