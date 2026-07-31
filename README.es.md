<div align="center">

# radio-dj

**Una radio 24/7 con DJ por IA en un único binario de Go.**
Apuntala tu carpeta de música, poné tu propia key de LLM, y transmite un
stream Icecast MP3 continuo sobre el que el DJ habla — en vivo, en medio
de la canción.

[![Licencia: MIT](https://img.shields.io/badge/Licencia-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![macOS](https://img.shields.io/badge/Plataforma-macOS%20%7C%20Linux-lightgrey)]()
[![LinkedIn](https://img.shields.io/badge/Seguir-AlmanzaTech-0A66C2?logo=linkedin&logoColor=white)](https://www.linkedin.com/in/almanzatech/)

[Español](README.es.md) · English

</div>

---

<img src="docs/screenshots/cassette-running.gif" alt="radio-dj cassette girando" align="right" width="320" />

`radio-dj` es un único binario de Go (~10 MB) que arma una estación de radio
completa. Elige temas de tu biblioteca, un DJ con IA habla entre canciones —
presenta el artista, lee la hora y el **clima real**, tira **datos curiosos**
con búsqueda web, y agradece los pedidos en vivo — y transmite un stream
Icecast MP3 estándar que cualquier reproductor puede sintonizar.

Sin Docker. Sin pesado. **~50 MB de RAM** — corre en una Raspberry Pi.

<br clear="right" />

## ✨ Qué hace

- **Radio 24/7** — un stream Icecast MP3 continuo (192 kbps) que no se corta.
- **DJ con IA** que habla entre canciones — intros, hora, clima real, datos
  curiosos (búsqueda web) y pedidos en vivo. Voz en cualquier idioma.
- **Ducking en vivo** — el DJ puede hablar *sobre* la música
  (`sidechaincompress`), estilo mezcladora hardware.
- **Pedidos en vivo** — los oyentes piden canciones desde la web o la API, con
  un nombre que el DJ lee al aire.
- **UI neo-brutalist** — cassette animado, "ahora suena" + "siguiente", tres
  paneles (historial, log del locutor, pedidos).
- **Skills editables** — reprogramá los segmentos del DJ como archivos de
  texto. Sin recompilar.
- **LLM BYOK** — GLM, OpenAI, OpenRouter, Ollama, Groq, o cualquier proveedor
  compatible con OpenAI.
- **Siempre-on** — se instala como servicio de macOS (launchd) y sobrevive a
  reinicios.

---

## 📸 Capturas

| DJ al aire (desktop) | Mobile |
|:---:|:---:|
| <img src="docs/screenshots/dj-onair.png" alt="DJ al aire — UI desktop con cassette animado" width="100%" /> | <img src="docs/screenshots/mobile.png" alt="Vista mobile" width="280" /> |

| Pedidos en vivo |
|:---:|
| <img src="docs/screenshots/requests.png" alt="Panel de pedidos — los oyentes piden canciones con su nombre" width="100%" /> |

---

## 🚀 Instalación rápida (macOS, Apple Silicon)

```bash
# 1. dependencias del sistema
brew install icecast ffmpeg
pipx install edge-tts          # cualquier TTS que escriba un archivo sirve

# 2. compilá
git clone https://github.com/johncrash64/radio-dj.git
cd radio-dj && go build -o radio-dj .

# 3. configurá (o saltate y usá el wizard de onboarding al primer arranque)
export ZAI_API_KEY=tu_key            # GLM-5.2 (cualquier OpenAI-compatible sirve)
export RDJ_LIBRARY=~/Music/library   # tu carpeta de música
export RDJ_LOCATION="La Paz"         # para la hora y el clima
export RDJ_VOICE_CMD="edge-tts --voice es-CO-SalomeNeural --text {text} --write-media {out}"

# 4. instalalo como servicio (siempre-on)
./radio-dj install
```

Listo. Abrí **http://localhost:7710** (UI) y **http://localhost:7702/stream.mp3** (stream).
Desinstalar: `./radio-dj install`.

> **Instalación de una línea** (descarga un binario precompilado o compila desde fuente):
> `curl -fsSL https://github.com/johncrash64/radio-dj/raw/master/install.sh | bash`

---

## 🎙️ El DJ

El DJ lo maneja un **Director con LLM** que planea cada *tanda* en una sola
llamada estructurada — arma la lista y decide qué decir entre canciones:

- **Intros de temas** — "Ahora suena *Canción* de *Artista*, del disco *Álbum*…"
- **IDs de estación** — nombre, ubicación, cantidad de oyentes.
- **Hora y clima** — reales, según tu ubicación.
- **Datos curiosos** — búsqueda web vía el LLM, o la API gratuita de Wikipedia.
- **Pedidos** — "Esta se la pedió *María* — *pidió que…*"

Cada línea de voz: el **LLM** escribe el texto (con contexto del tema) → **tu
TTS** lo sintetiza → entra al stream antes o durante el tema. Los segmentos
mid-song usan `sidechaincompress` para bajar la música mientras habla el DJ.

---

## 🧩 Skills (editables)

Los segmentos del DJ viven como archivos de texto bajo `~/.radio-dj/skills/`.
Editá los que vienen o agregá los tuyos — radio-dj los carga al arrancar. Sin
recompilar.

---

## 📡 API (para tu app mobile, PanelHUD, Home Assistant, etc.)

HTTP simple — funciona con cualquier cliente:

| Método | Ruta | Descripción |
|---|---|---|
| `GET` | `/now-playing` | tema actual + siguiente + pedidos + estado |
| `POST` | `/request` | `{"from":"María","text":"Bohemian Rhapsody"}` — pedir una canción |
| `GET` | `/stream.mp3` | el stream de audio (Icecast) |
| `GET` | `/listen.pls` / `/listen.m3u` | playlist para Sonos/VLC/auto |
| `GET` | `/health` | liveness |

El stream es una **URL Icecast / HTTP-MP3 estándar** — pegala en VLC, mpv,
cualquier navegador, TuneIn, Radio Garden, Sonos, o apps de radio de
iOS/Android.

Referencia completa: **[docs/API.md](docs/API.md)** · **[docs/openapi.yaml](docs/openapi.yaml)**.

---

## ⚙️ Configuración (variables `RDJ_*`)

| Variable | Default | Qué |
|---|---|---|
| `RDJ_LIBRARY` | `~/Music/library` | carpeta de música |
| `RDJ_SOURCE` | `folder` | `folder` o `navidrome` |
| `RDJ_NAVIDROME_URL/USER/PASS` | — | fuente Navidrome (opcional) |
| `ZAI_API_KEY` | — | tu key de LLM (BYOK) |
| `RDJ_GLM_MODEL` | `glm-5.2` | modelo |
| `RDJ_VOICE_CMD` | — | comando TTS (`{text}` y `{out}`) |
| `RDJ_LOCATION` / `RDJ_LAT` / `RDJ_LON` | La Paz | para hora/clima |
| `RDJ_DJ_EVERY` | `2` | el DJ habla cada N temas |
| `RDJ_CHUNK` | `8` | temas por tanda (afecta el prefetch) |
| `RDJ_BITRATE` | `192` | bitrate del stream (kbps) |
| `RDJ_BED` | — | instrumental para ducking (DJ sobre música) |

---

## 🛠️ Cómo funciona (arquitectura)

```
radio-dj (Go, ~50 MB RAM)
  ├─ supervisor   levanta y vigila icecast (child process, auto-restart)
  ├─ producer     arma la próxima tanda (Director LLM + TTS) en prefetch
  ├─ master ffmpeg (persistente)   PCM → MP3 → icecast, NUNCA se reconecta
  └─ decoders ffmpeg (por segmento)   tema/voz → PCM al master
```

Una sola conexión a icecast que no se cae = cero cortes para los oyentes.

```
  ┌──────────┐   PCM    ┌──────────────┐  MP3   ┌──────────┐
  │ producer │─────────▶│ master ffmpeg │──────▶│ icecast  │──▶ oyentes
  └──────────┘          └──────────────┘        └──────────┘
       │                        ▲
       │ TTS voz                │ PCM (fd4)
       ▼                        │
  ┌──────────┐          ┌───────────────┐
  │ LLM + TTS │          │ sidechaincomp │  ← baja la música para la voz
  └──────────┘          └───────────────┘
```

---

## 🪶 Licencia

**MIT** — hacé con esto lo que quieras. Ver [LICENSE](LICENSE).

---

<div align="center">

Hecho por **[AlmanzaTech](https://www.linkedin.com/in/almanzatech/)** ·
[Reportar un bug](https://github.com/johncrash64/radio-dj/issues) ·
[Pedir una feature](https://github.com/johncrash64/radio-dj/issues)

</div>
