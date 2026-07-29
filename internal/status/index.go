package status

import (
	_ "embed"
	"text/template"
	"net/http"
)

// indexPage — neo-brutalist player: thick borders, hard offset shadows, bold
// blocky sections, pastel fills with dark frames. Cassette has real reel
// windows (spinning) + tape strip — no fake "flow" animation. Polls
// /now-playing (current + next + requests), POSTs /request. No deps.

//go:embed templates/index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

func serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTmpl.Execute(w, nil)
}
