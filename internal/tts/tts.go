// Package tts maps a (provider, voice) pair to the shell command that
// synthesizes text to an audio file — so users pick a provider + voice in the
// onboarding UI instead of typing a raw command. Add a provider here and it
// shows up everywhere.
package tts

import (
	"fmt"
	"strings"
)

// Provider is a known TTS backend.
type Provider struct {
	ID      string   // "edge-tts"
	Label   string   // "Microsoft Edge TTS"
	Voices  []Voice  // a curated starter list; users can type any
	Comment string   // install hint
}

// Voice is one selectable voice for a provider.
type Voice struct {
	ID    string // "es-CO-SalomeNeural"
	Label string // "Salomé (Colombia)"
}

// Providers is the catalog the onboarding UI renders.
var Providers = []Provider{
	{
		ID:    "edge-tts",
		Label: "Microsoft Edge TTS (cloud, high quality)",
		Voices: []Voice{
			{"es-CO-SalomeNeural", "Salomé · Colombia"},
			{"es-CO-GonzaloNeural", "Gonzalo · Colombia"},
			{"es-MX-DaliaNeural", "Dalia · México"},
			{"es-ES-ElviraNeural", "Elvira · España"},
			{"es-ES-AlvaroNeural", "Álvaro · España"},
			{"es-AR-ElenaNeural", "Elena · Argentina"},
			{"en-US-AriaNeural", "Aria · English US"},
		},
		Comment: "pipx install edge-tts",
	},
	{
		ID:    "piper",
		Label: "Piper (local, offline)",
		Voices: []Voice{
			{"es_ES-davefx-medium", "Davefx · España"},
			{"es_ES-sharcs-medium", "Sharcs · España"},
			{"es_MX-almawavlu-medium", "Almawavlu · México"},
		},
		Comment: "brew install piper-tts  (or download a voice model)",
	},
	{
		ID:    "say",
		Label: "macOS say (built-in, no install)",
		Voices: []Voice{
			{"Monica", "Monica (es)"},
			{"Paulina", "Paulina (es)"},
			{"Diego", "Diego (es)"},
		},
		Comment: "system built-in",
	},
}

// BuildCommand returns the shell command that synthesizes `text` to `outFile`,
// for the given provider+voice. {text}/{out} are shell-quoted.
func BuildCommand(provider, voice, text, outFile string) (string, error) {
	t := quote(text)
	o := quote(outFile)
	switch provider {
	case "edge-tts":
		v := voice
		if v == "" {
			v = "es-CO-SalomeNeural"
		}
		return fmt.Sprintf("edge-tts --voice %s --text %s --write-media %s", v, t, o), nil
	case "piper":
		v := voice
		if v == "" {
			v = "es_ES-davefx-medium"
		}
		// piper reads text from stdin
		return fmt.Sprintf("echo %s | piper -m %s -f %s", t, v, o), nil
	case "say":
		// macOS say → aiff, then it's a valid decoder input downstream
		return fmt.Sprintf("say -v %s %s -o %s", voice, t, o), nil
	default:
		return "", fmt.Errorf("unknown TTS provider %q", provider)
	}
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
