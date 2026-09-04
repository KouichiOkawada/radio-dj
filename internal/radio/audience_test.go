package radio

import "testing"

func TestAudienceGateRequiresKnownPositiveListenerCount(t *testing.T) {
	g := &audienceGate{}
	if g.Active() {
		t.Fatal("unknown audience must not permit paid AI")
	}
	if !g.set(0) || g.Active() {
		t.Fatal("zero listeners must keep paid AI paused")
	}
	if !g.set(1) || !g.Active() {
		t.Fatal("one listener must resume paid AI")
	}
	if g.set(2) == true {
		t.Fatal("positive-to-positive count change should not toggle the gate")
	}
	if !g.set(0) || g.Active() {
		t.Fatal("return to zero must pause paid AI")
	}
}
