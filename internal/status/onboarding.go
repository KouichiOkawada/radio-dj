package status

import (
	_ "embed"
	"text/template"
	"net/http"
)

// onboardingPage — first-run wizard. Tube-radio cabinet: warm charcoal
// background, amber FM-dial glow, cream text, reusing the player's exact
// neobrutalist tokens (3px borders, 6px hard offset shadows, Helvetica Neue).
// Native <select>s are hidden in place (ids preserved) and replaced by an
// accessible custom dropdown that two-way-syncs .value + dispatches change.
// Language, music source, BYOK LLM (provider presets + URL + key + model +
// live Test), voice (provider + voice). POSTs JSON to /onboarding.

//go:embed templates/onboarding.html
var onboardingHTML string

var onboardingTmpl = template.Must(template.New("onboarding").Parse(onboardingHTML))

func serveOnboarding(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = onboardingTmpl.Execute(w, nil)
}
