# radio-dj

> A personal 24/7 internet radio station with an AI DJ that talks between songs.
> Lightweight, local-first, and as simple as pointing it at your music folder.

**[Español](README.es.md)** · English

`radio-dj` is a single Go binary that runs a radio station: it picks tracks
from your library, an AI DJ speaks between songs (intros the artist, reads the
**weather**, drops **curiosity facts** with web search, acknowledges
requests), and broadcasts a continuous MP3 stream anyone can tune into. No
Docker, no bloat — ~50 MB of RAM.

---

## ✨ Features

- **24/7 radio** with a continuous Icecast MP3 stream (192 kbps) that doesn't drop.
- **AI DJ** speaking between songs (Spanish voice, configurable): track intros,
  **time**, **real weather**, **curiosity facts** (web search), request shoutouts.
- **Live requests** — listeners request songs from the web UI or the API.
- **Neo-brutalist UI** with an animated cassette, now-playing + next.
- **Editable skills** — add or improve the DJ's segments without recompiling.
- **Always-on** — installs as a macOS launchd service, survives reboots.

---

## 🚀 Quick start (macOS, Apple Silicon)

```bash
# 1. system deps
brew install icecast ffmpeg
pipx install edge-tts          # any TTS that writes a file works

# 2. build
git clone https://github.com/<you>/radio-dj.git
cd radio-dj && go build -o radio-dj .

# 3. configure (or skip and use the onboarding wizard at first run)
export ZAI_API_KEY=your_key             # GLM-5.2 (any OpenAI-compatible works)
export RDJ_LIBRARY=~/Music/library      # your music folder
export RDJ_LOCATION="La Paz"            # for time + weather
export RDJ_VOICE_CMD="edge-tts --voice es-CO-SalomeNeural --text {text} --write-media {out}"

# 4. install as an always-on service
./radio-dj install
```

Then open **http://localhost:7710** (UI) and **http://localhost:7702/stream.mp3** (stream).
Uninstall: `./radio-dj uninstall`.

---

## 🎙️ The DJ

- Speaks every **`RDJ_DJ_EVERY`** songs (default 2).
- **Alternates**: track intro ↔ station-id / time / weather / curiosity.
- Each voice: **GLM** writes the text (with track context) → **your TTS**
  synthesizes it → it airs before the track.
- Curiosity facts use GLM's **web search** (or the free Wikipedia API).

---

## 🧩 Skills (editable)

The DJ's segments live as text files under `~/.radio-dj/skills/`. Edit the
built-ins or add your own — radio-dj loads them at startup. No recompiling.

---

## 📡 API

| Method | Path | Description |
|---|---|---|
| `GET` | `/now-playing` | current + next track, requests, status |
| `POST` | `/request` | `{"text":"..."}` — request a song |
| `GET` | `/stream.mp3` | the audio stream (Icecast) |
| `GET` | `/listen.pls` · `/listen.m3u` | playlist for Sonos/VLC/car |
| `GET` | `/health` | liveness |

Full reference + integration recipes (mobile, PanelHUD, Home Assistant):
see **[docs/API.md](docs/API.md)**.

---

## ⚙️ Configuration

All `RDJ_*` env vars (or the onboarding wizard's persisted config). Key ones:
`RDJ_LIBRARY`, `ZAI_API_KEY`, `RDJ_LOCATION`, `RDJ_VOICE_CMD`, `RDJ_DJ_EVERY`,
`RDJ_SOURCE` (`folder` | `navidrome`), `RDJ_BED` (ducking instrumental).
See [README.es.md](README.es.md) for the full table.

---

## 🛠️ Architecture

```
radio-dj (Go, ~50MB)
  ├─ supervisor: spawns + watches icecast (child, auto-restart)
  ├─ producer (goroutine): builds next batch (GLM + TTS) via prefetch
  ├─ master ffmpeg (persistent): PCM → MP3 → icecast, never reconnects
  └─ decoders ffmpeg (per segment): track/voice → PCM into the master
```

One icecast connection that never drops = zero listener cutouts.

## 🪶 License

MIT.

<!-- tevunah: if ~/.tevunah exists, radio-dj nests into that ecosystem
     silently (agentic-os tevunah — coming, maybe released later). -->
