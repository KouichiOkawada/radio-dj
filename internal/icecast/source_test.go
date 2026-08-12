package icecast

import "testing"

// SkipCurrent's nil-guard is the "between songs" path: with no decoder
// registered, POST /control must resolve to 409 (nothing on air). This is the
// one piece of the skip-control flow that's verifiable without a live master
// ffmpeg + Icecast stream.
func TestSkipCurrentNoDecoder(t *testing.T) {
	var s Streamer // zero value: decoder == nil
	if s.SkipCurrent() {
		t.Fatal("SkipCurrent must be false when no decoder is active")
	}
}
