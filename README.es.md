# radio-dj

**Español** · [English](README.md)

> Una radio personal 24/7 con un DJ por IA que habla entre canciones. Liviana,
> local-first, y tan simple como apuntarla a tu carpeta de música.

`radio-dj` es un único binario (Go) que arma una estación de radio por internet:
elige temas de tu biblioteca, un DJ con IA presenta entre canciones (habla del
artista, lee el clima, tira datos curiosos), y transmite un stream MP3 continuo
que cualquiera puede sintonizar. Sin Docker, sin pesado — corre con ~50 MB de
RAM.

---

## ✨ Qué hace

- **Radio 24/7** con un stream Icecast MP3 (192 kbps) que no se corta.
- **DJ con IA** que habla entre canciones (voz en español, configurable).
  Presenta temas, lee la **hora** y el **clima real**, tira **datos curiosos**
  (con búsqueda web) y agradece los pedidos.
- **Pedidos en vivo**: los oyentes piden canciones desde la web o la API.
- **UI neo-brutalist** con un cassette animado, "ahora suena" y "siguiente".
- **Skills editables**: agregá o mejorá los segmentos del DJ sin tocar código.
- **Siempre-on**: se instala como servicio de macOS (launchd) y sobrevive a
  reinicios y cierre de terminal.

---

## 🚀 Instalación rápida (macOS, Apple Silicon)

### 1. Dependencias del sistema
```bash
brew install icecast ffmpeg
# voz (cualquier TTS que escriba un archivo funcione; ejemplo con edge-tts):
pipx install edge-tts
```

### 2. radio-dj
```bash
git clone https://github.com/<tu-usuario>/radio-dj.git
cd radio-dj && go build -o radio-dj .
```

### 3. Configurá (variables de entorno)
```bash
export ZAI_API_KEY=tu_key            # GLM-5.2 (cualquier OpenAI-compatible sirve)
export RDJ_LIBRARY=~/Music/library   # tu carpeta de música
export RDJ_LOCATION="La Paz"         # para la hora y el clima
export RDJ_VOICE_CMD="edge-tts --voice es-CO-SalomeNeural --text {text} --write-media {out}"   # tu TTS
```

### 4. Instalalo como servicio (siempre-on)
```bash
./radio-dj install
```
Listo. Corre en background, se reinicia solo, sobrevive al reinicio.
- UI: **http://localhost:7710**
- Stream: **http://localhost:7702/stream.mp3**

Para desinstalar: `./radio-dj uninstall`.

---

## 🎙️ El DJ: cada cuánto habla y cómo

- Habla cada **`RDJ_DJ_EVERY`** canciones (default 2). Cambialo con la variable.
- **Alterna** segmentos: intro del tema ↔ estación/hora/clima/curiosidad.
- Cada voz se genera así: **GLM** escribe el texto (con el contexto del tema) →
  **tu TTS** (piper/qohl) lo sintetiza → entra al stream antes del tema.
- Los datos curiosos usan **web_search** de GLM (o Wikipedia API sin key).

---

## 🧩 Skills (editables)

Los segmentos del DJ viven como archivos de texto bajo `~/.radio-dj/skills/`.
Editá los que vienen o agregá los tuyos — radio-dj los carga al arrancar. Sin
recompilar.

---

## 📡 API (para tu app mobile, PanelHUD, Home Assistant, etc.)

Todo es HTTP simple:

| Método | Ruta | Descripción |
|---|---|---|
| `GET` | `/now-playing` | tema actual + siguiente + pedidos + estado |
| `POST` | `/request` | `{"text":"..."}` — pedir una canción |
| `GET` | `/stream.mp3` | el stream de audio (Icecast) |
| `GET` | `/listen.pls` / `/listen.m3u` | playlist para Sonos/VLC/auto |
| `GET` | `/health` | liveness |

**Receta PanelHUD (Swift):** hacé poll a `/now-playing` cada 5s, mostrá el tema
actual + cola, y `POST /request` para pedir desde el notch. El audio va a
`<audio src="http://<host>:7702/stream.mp3">`.

---

## ⚙️ Configuración (variables `RDJ_*`)

| Variable | Default | Qué |
|---|---|---|
| `RDJ_LIBRARY` | `~/Music/library` | carpeta de música |
| `RDJ_SOURCE` | `folder` | `folder` o `navidrome` |
| `RDJ_NAVIDROME_URL/USER/PASS` | — | si usás Navidrome como fuente |
| `ZAI_API_KEY` | — | tu key de GLM (BYOK) |
| `RDJ_GLM_MODEL` | `glm-5.2` | modelo |
| `RDJ_VOICE_CMD` | — | comando TTS (`{text}` y `{out}`) |
| `RDJ_LOCATION` / `RDJ_LAT` / `RDJ_LON` | La Paz | para hora/clima |
| `RDJ_DJ_EVERY` | `2` | el DJ habla cada N temas |
| `RDJ_CHUNK` | `8` | temas por tanda (afecta el prefetch) |
| `RDJ_BITRATE` | `192` | kbps del stream |
| `RDJ_BED` | — | instrumental para ducking (DJ sobre música) |

---

## 🛠️ Cómo funciona (arquitectura)

```
radio-dj (Go, ~50MB RAM)
  ├─ supervisor: levanta y vigila icecast (child process, auto-restart)
  ├─ producer (goroutine): arma la próxima tanda (GLM + TTS) en prefetch
  ├─ master ffmpeg (persistente): PCM → MP3 → icecast, NUNCA se reconecta
  └─ decoders ffmpeg (por segmento): tema/voz → PCM al master
```

Una sola conexión a icecast que no se cae = cero cortes para los oyentes.

---

## 🪶 Licencia

MIT. Hacé con esto lo que quieras.

---

<!-- easter egg: si ~/.tevunah existe, radio-dj se anida en su ecosistema
     (agentic-os tevunah — coming, maybe released later). Integración
     silenciosa, sin ruido en la UI. -->
