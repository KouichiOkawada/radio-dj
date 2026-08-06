// Package codec resolves the ffmpeg audio encoder and derives the stream
// metadata (muxer, content-type, mount suffix) that every downstream layer
// needs. The default encoder is platform-aware: aac_at (Apple AudioToolbox,
// hardware-accelerated) on macOS, ffmpeg's built-in aac everywhere else (always
// present, no external lib). Override with the RDJ_ENCODER env var.
//
// Keeping the encoder→{format, content-type, mount} mapping in one place is
// what lets the icecast mount-name, the reverse proxy and the web <audio> src
// all follow the same codec — so switching encoders never desyncs the player.
package codec

import (
	"os"
	"runtime"
)

// Meta is everything downstream layers derive from the chosen encoder.
type Meta struct {
	Encoder     string // ffmpeg -c:a value
	Format      string // ffmpeg -f output muxer ("adts" | "mp3")
	ContentType string // icecast -content_type ("audio/aac" | "audio/mpeg")
	Suffix      string // mount file extension (".aac" | ".mp3")
}

var table = map[string]Meta{
	"aac_at":     {"aac_at", "adts", "audio/aac", ".aac"},
	"aac":        {"aac", "adts", "audio/aac", ".aac"},
	"libmp3lame": {"libmp3lame", "mp3", "audio/mpeg", ".mp3"},
}

// Default returns the platform default encoder. aac_at is macOS-only
// (AudioToolbox); every other OS gets ffmpeg's built-in aac.
func Default() string {
	if runtime.GOOS == "darwin" {
		return "aac_at"
	}
	return "aac"
}

// Resolve returns RDJ_ENCODER if set, else the platform default.
func Resolve() string {
	if v := os.Getenv("RDJ_ENCODER"); v != "" {
		return v
	}
	return Default()
}

// MetaFor returns the stream metadata for an encoder. Unknown encoders fall
// back to the universal aac default so a bad override degrades rather than
// breaking the stream entirely.
func MetaFor(enc string) Meta {
	if m, ok := table[enc]; ok {
		return m
	}
	return table["aac"]
}
