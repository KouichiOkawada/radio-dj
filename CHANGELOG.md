# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.0] — 2026-08-06

### Added
- **Configurable, cross-platform broadcast encoder.** The audio encoder is now
  platform-aware and overridable via `RDJ_ENCODER`:
  - **macOS** → `aac_at` (Apple AudioToolbox, **hardware-accelerated**) — ~128×
    realtime, dramatically lower CPU than software MP3.
  - **Linux / Windows** → `aac` (ffmpeg's built-in, always present — no extra lib).
  - **Override:** `RDJ_ENCODER=libmp3lame` (or any ffmpeg audio encoder).
- New `internal/codec` package — the single source of truth mapping an encoder
  to its stream metadata (muxer, content-type, mount suffix). The icecast
  `<mount-name>`, the reverse-proxy route, and the web `<audio>` src all derive
  from it, so the player can never desync from the stream.

### Changed
- **Default stream mount is now `/stream.aac`** (was `/stream.mp3`). The mount
  follows the encoder (`aac*` → `.aac`, `libmp3lame` → `.mp3`). Listeners with a
  hardcoded `/stream.mp3` URL: either update it, or set `RDJ_ENCODER=libmp3lame`
  to keep MP3.
- **Default bitrate stays 192 kbps.** AAC at 192 kbps transparently beats MP3 at
  the same rate; lower it with `RDJ_BITRATE` if you prefer.

### Fixed
- **Web player could 404 after an encoder switch.** The `<audio>` src was
  hardcoded to `stream.mp3` in the page JS, so it desynced from the reverse
  proxy whenever the mount changed. It now follows `{{.StreamPath}}` from config.

### Performance
- The always-on broadcast encoder is **~1.6× faster** on Apple Silicon
  (`aac_at` 128× vs `libmp3lame` ~78× realtime). Note: the live DJ ducking
  filtergraph (`sidechaincompress` + `amix`) remains the bulk of the master
  process CPU by design — that's the cost of real-time voice-over ducking.

## [0.3.1]

- UI: render legacy dj-log lines without stray bracket.
