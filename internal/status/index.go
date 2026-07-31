package status

import (
	_ "embed"
	"net/http"
	"text/template"
)

// indexPage — neo-brutalist player: thick borders, hard offset shadows, bold
// blocky sections, pastel fills with dark frames. Cassette has real reel
// windows (spinning) + tape strip — no fake "flow" animation. Polls
// /now-playing (current + next + requests), POSTs /request. No deps.

//go:embed templates/index.html
var indexHTML string

//go:embed templates/permanent-marker.woff2
var markerFont []byte

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

func serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTmpl.Execute(w, nil)
}

// serveFont serves the self-hosted Permanent Marker woff2 (30KB) with a
// 1-year immutable cache — same origin, works offline, no FOUT.
func serveFont(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(markerFont)
}
