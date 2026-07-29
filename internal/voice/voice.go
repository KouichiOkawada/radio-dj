// Package voice synthesizes DJ banter to an audio file. Users pick a
// provider+voice in onboarding (edge-tts/piper/say); power users can still set
// a raw command template (RDJ_VOICE_CMD) which overrides.
package voice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"radio-dj/internal/tts"
)

type Voice struct {
	provider string
	voice    string
	rawCmd   string
}

// New: provider+voice is the normal path; rawCmd overrides when set.
func New(provider, voice, rawCmd string) *Voice {
	return &Voice{provider: provider, voice: voice, rawCmd: rawCmd}
}

// Speak renders text to a temp audio file and returns its path.
func (v *Voice) Speak(text string) (string, error) {
	out := filepath.Join(os.TempDir(), fmt.Sprintf("radio-dj-voice-%d.wav", time.Now().UnixNano()))
	cmd, err := v.commandFor(text, out)
	if err != nil {
		return "", err
	}
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return "", fmt.Errorf("voice cmd failed: %w", err)
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("voice produced no file at %s", out)
	}
	return out, nil
}

func (v *Voice) commandFor(text, outFile string) (string, error) {
	if v.rawCmd != "" {
		c := strings.ReplaceAll(v.rawCmd, "{text}", shellQuote(text))
		return strings.ReplaceAll(c, "{out}", shellQuote(outFile)), nil
	}
	if v.provider != "" {
		return tts.BuildCommand(v.provider, v.voice, text, outFile)
	}
	return "", fmt.Errorf("no voice configured (set a provider+voice or RDJ_VOICE_CMD)")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
