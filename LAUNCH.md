# radio-dj — Launch Playbook

> Internal doc. Keeps the Reddit post, README, and any future landing on the
> same message. If you change the positioning, change it here first.

## One-liner (the elevator pitch)

> A 24/7 AI-DJ internet radio as a single Go binary (~10 MB, ~50 MB RAM, no
> Docker). Point it at your music folder, bring your own LLM key, and it
> broadcasts a standard Icecast MP3 stream the DJ talks over — live, mid-song.

## Positioning — a complement, not a competitor

radio-dj does **not** compete with [SUB/WAVE](https://getsubwave.com). SUB/WAVE
is a beautiful, productized radio: themes, admin dashboard, skills marketplace,
an embeddings-based music graph, native apps, an MCP server — the full operator
experience. Go use it if you want the product.

radio-dj is a different bet: **what is the smallest, most readable stack that is
still a real 24/7 AI DJ?** It's an architectural experiment aimed at people who
want to *read* the code, understand it, and extend it — not operators who want a
turnkey dashboard.

**Rule:** compare **philosophy and footprint**, never feature lists. On a
feature list radio-dj loses (and that's fine — it's not trying to win there).

## Differentiators (frame as philosophy, not as "more features")

| | SUB/WAVE | radio-dj |
|---|---|---|
| Bet | The full dashboard | The minimum viable elegance |
| Form | Docker Compose, 5 containers | **1 Go binary, ~10 MB** |
| Footprint | Hundreds of MB (5 containers) | **~50 MB RAM** — runs on a Raspberry Pi |
| Dependencies | Docker / Colima | `ffmpeg` + `icecast` + `edge-tts` (no Docker) |
| AI | Its own setup | **BYOK** — GLM / OpenAI / OpenRouter / Ollama / Groq / custom |
| DJ overlay | Between songs | **Live, mid-song** (ffmpeg `sidechaincompress`, hardware-mixer style) |
| Banter | Built-in | **Editable `.md` skills** — reprogram the DJ without recompiling |
| Music source | Navidrome (+ embeddings graph) | **Local folder OR Navidrome** |
| Audience | Operators | Tinkerers / architects / Go readers |

## Connectivity — the honest differentiator that's actually in your favour

radio-dj outputs a **standard Icecast / HTTP-MP3 stream**. SUB/WAVE's manual says
the same thing about itself: *"Tune in from the native apps, **VLC, or any app
that opens an internet-radio stream**."* There is no proprietary protocol.

So the `http://your-host:7702/stream.mp3` URL is universally connectable,
**today, with zero extra work**:

- **VLC, mpv, foobar, any browser** — paste the URL.
- **TuneIn / Radio Garden** — submit the stream URL via their broadcaster portal.
- **Sonos, Home Assistant, iOS/Android internet-radio apps** — add the URL.
- **A future branded app** — just point an audio player at the URL.

SUB/WAVE's native app is a polished client *on top of* the same universal stream.
radio-dj doesn't need one to be connectable; it would need one only for branded
UX parity, later.

## Reddit post — draft (English, ready to paste)

**Title (pick one):**

- `radio-dj — a 24/7 AI-DJ internet radio as a single Go binary (~50MB RAM, no Docker)`
- `I built an AI DJ radio station in one Go binary. It talks over songs live and runs on a Raspberry Pi.`

**Body:**

```
I've been enjoying SUB/WAVE — it's a genuinely polished AI radio with themes,
a skills marketplace, an embeddings-based music graph, the works. I didn't want
to compete with it. I got curious about the minimum instead:

  What's the smallest, most readable stack that's still a real 24/7 AI DJ?

radio-dj is my answer. One Go binary, ~10 MB, ~50 MB of RAM, no Docker.

What it does:
- Picks tracks from your music folder (or Navidrome), broadcasts a continuous
  Icecast MP3 stream.
- An AI DJ speaks between songs: intros the artist, reads the real weather,
  drops curiosity facts (web search), acknowledges requests.
- Talks OVER songs, live — ffmpeg sidechaincompress ducks the music while the
  DJ speaks, like a hardware mixer. Not just between tracks.
- Bring your own LLM key (GLM / OpenAI / OpenRouter / Ollama / Groq / custom).
- The DJ's banter prompts are editable .md files — reprogram the personality
  without recompiling.

Because it's a standard Icecast stream, you can listen in VLC, mpv, any
browser, or add it to TuneIn / Radio Garden / Sonos / Home Assistant by
submitting the stream URL.

It's a complement to SUB/WAVE, not a replacement. SUB/WAVE is the full
operator dashboard; radio-dj is for people who want to read elegant Go and
extend it.

Repo: https://github.com/johncrash64/radio-dj

Happy to answer questions / take feedback. It's early, so be kind. :)
```

## Launch checklist (do these BEFORE posting)

- [ ] **Make the repo public** — `gh repo edit johncrash64/radio-dj --visibility public`. A private repo link in a Reddit post is dead on arrival.
- [ ] **Add a screenshot/GIF of the player** to the README and the post. The neo-brutalist cassette is the visual hook; a text-only post sinks. Take it with `screencapture` of the player at `http://localhost:7710/`.
- [ ] **Verify the quick start works cold** — `go build && ./radio-dj serve` from a clean clone, follow the README exactly, confirm the stream comes up.
- [ ] **Decide subs** — best fit, in order: `r/golang` (Go binary, elegant architecture), `r/selfhosted` (single-binary, no Docker), then `r/SideProject` or an internet-radio community.

## Timing — don't post and sleep

Reddit posts live or die in the first **2–4 hours**. The algorithm weighs early
engagement, and the author replying to the first comments materially helps a
post climb. If you post and sleep:

- first questions sit unanswered for ~8 h,
- a broken link / install issue becomes the top comment with no author response,
- and by the time US waking-hours traffic arrives (Bolivia morning), the post is
  already buried in `new`.

**Post when you can be awake and watching for at least the first 2–4 hours.**
For Bolivia timezone, that means **morning-to-midday local** (catches US
morning/midday — peak traffic for the English-speaking tech subs).
