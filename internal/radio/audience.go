package radio

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"radio-dj/internal/status"
)

// audienceGate is the single cost-control switch for cloud AI. Unknown and
// zero listeners are inactive; only a confirmed Icecast count >= 1 permits a
// new OpenAI request.
type audienceGate struct {
	active atomic.Bool
	known  atomic.Bool
}

func (g *audienceGate) Active() bool { return g != nil && g.known.Load() && g.active.Load() }

func (g *audienceGate) set(count int) bool {
	wasKnown, wasActive := g.known.Swap(true), g.active.Swap(count > 0)
	return !wasKnown || wasActive != (count > 0)
}

func startAudienceMonitor(ctx context.Context, st *status.Server, gate *audienceGate, onChange func()) {
	if st == nil || gate == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			count, ok := st.PollListeners()
			if ok && gate.set(count) {
				if count > 0 {
					log.Printf("[audience] %d listener(s): OpenAI generation resumed", count)
				} else {
					log.Printf("[audience] no listeners: OpenAI generation paused")
				}
				if onChange != nil {
					onChange()
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
