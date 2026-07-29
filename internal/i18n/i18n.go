// Package i18n holds the DJ's copy and the default skill templates, keyed by
// language. Everything is embedded in the binary via //go:embed so radio-dj
// stays a single file with no runtime file dependencies for its defaults.
//
// Three homes for text, by design:
//   - system persona + ask templates + weather words → prompts/{lang}.json
//     (compiled in; structural, not user-editable at runtime)
//   - skill flavor (station-id / time / curiosity)    → skills/{lang}/*.md
//     (seeded to the state dir on first boot, then user-editable — overriding
//     these is the feature)
//
// Language comes from config.Language ("es" | "en"); any unknown value falls
// back to "es".
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed prompts/*.json
var promptFS embed.FS

//go:embed all:skills
var skillFS embed.FS

// Prompts is a flat key→string set parsed from prompts/{lang}.json.
type Prompts map[string]string

// Load returns the prompt set for lang (falls back to "es" on any miss).
func Load(lang string) (Prompts, error) {
	lang = resolveLang(lang)
	data, err := promptFS.ReadFile("prompts/" + lang + ".json")
	if err != nil {
		return nil, fmt.Errorf("i18n: read prompts/%s.json: %w", lang, err)
	}
	var p Prompts
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("i18n: parse prompts/%s.json: %w", lang, err)
	}
	return p, nil
}

// Get returns one prompt; "" if the key is absent.
func (p Prompts) Get(key string) string { return p[key] }

// Sub returns the prompt at key with {placeholders} filled from kv.
func (p Prompts) Sub(key string, kv map[string]string) string {
	s := p[key]
	for k, v := range kv {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// SeedSkills copies the embedded default skill .md files for lang into
// dir/skills/, leaving any file the user already has untouched. After this,
// dir/skills/ holds the full editable default set. Safe to call every boot.
func SeedSkills(lang, dir string) error {
	base := "skills/" + resolveLang(lang)
	entries, err := skillFS.ReadDir(base)
	if err != nil {
		return fmt.Errorf("i18n: seed %s: %w", base, err)
	}
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		dest := filepath.Join(skillsDir, e.Name())
		if _, statErr := os.Stat(dest); statErr == nil {
			continue // user owns this file now — never overwrite
		}
		if data, rerr := skillFS.ReadFile(base + "/" + e.Name()); rerr == nil {
			_ = os.WriteFile(dest, data, 0o644)
		}
	}
	return nil
}

// resolveLang returns lang if its prompt file exists, else "es".
func resolveLang(lang string) string {
	if lang != "" {
		if _, err := promptFS.ReadFile("prompts/" + lang + ".json"); err == nil {
			return lang
		}
	}
	return "es"
}
