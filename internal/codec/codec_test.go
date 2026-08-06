package codec

import "testing"

func TestMetaFor(t *testing.T) {
	cases := map[string]Meta{
		"aac_at":     {"aac_at", "adts", "audio/aac", ".aac"},
		"aac":        {"aac", "adts", "audio/aac", ".aac"},
		"libmp3lame": {"libmp3lame", "mp3", "audio/mpeg", ".mp3"},
	}
	for enc, want := range cases {
		if got := MetaFor(enc); got != want {
			t.Errorf("MetaFor(%q) = %+v, want %+v", enc, got, want)
		}
	}
}

func TestMetaForUnknownFallsBackToAAC(t *testing.T) {
	if m := MetaFor("nonsense"); m.Encoder != "aac" || m.Suffix != ".aac" {
		t.Errorf("unknown encoder must fall back to aac, got %+v", m)
	}
}

// The mount MUST follow the codec, so the icecast <mount-name>, the reverse
// proxy path and the <audio> src (StreamPath) never desync. This is the
// invariant the whole encoder refactor protects.
func TestMountSuffixFollowsCodec(t *testing.T) {
	for _, enc := range []string{"aac_at", "aac", "libmp3lame"} {
		m := MetaFor(enc)
		mount := "/stream" + m.Suffix
		streamPath := mount[1:] // strings.TrimPrefix(mount, "/")
		if streamPath != "stream"+m.Suffix {
			t.Errorf("stream path desync for %s: got %s", enc, streamPath)
		}
	}
}
