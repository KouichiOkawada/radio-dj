package status

import (
	_ "embed"
	"text/template"
	"net/http"
)

// onboardingPage — first-run wizard. Shares the player's exact neobrutalist
// design tokens (cream paper bg, 3px borders, hard offset shadows, same
// --font-ui/mono/hand + color vars). Native <select>s are hidden in place
// (ids preserved) and replaced by an accessible custom dropdown that
// two-way-syncs .value + dispatches change. Language, music source, BYOK LLM
// (provider presets + URL + key + model + live Test), voice (provider +
// voice). POSTs JSON to /onboarding.

//go:embed templates/onboarding.html
var onboardingHTML string

var onboardingTmpl = template.Must(template.New("onboarding").Parse(onboardingHTML))

func serveOnboarding(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = onboardingTmpl.Execute(w, nil)
}
