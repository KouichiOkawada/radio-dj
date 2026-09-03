package musicfeed

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedLicense(t *testing.T) {
	for _, value := range []string{
		"https://creativecommons.org/publicdomain/zero/1.0/",
		"https://creativecommons.org/licenses/by/4.0/",
		"https://creativecommons.org/licenses/by-sa/4.0/",
	} {
		if !allowedLicense(value) {
			t.Fatalf("expected allowed: %s", value)
		}
	}
	for _, value := range []string{
		"https://creativecommons.org/licenses/by-nc/4.0/",
		"https://creativecommons.org/licenses/by-nd/4.0/",
		"",
	} {
		if allowedLicense(value) {
			t.Fatalf("expected rejected: %s", value)
		}
	}
}

func TestCompleteVocalMix(t *testing.T) {
	if !isCompleteVocalMix("media,remix,female_vocals,audio,mp3") {
		t.Fatal("complete vocal remix rejected")
	}
	for _, tags := range []string{"media,acappella,female_vocals", "media,remix,instrumental", "media,sample,female_vocals"} {
		if isCompleteVocalMix(tags) {
			t.Fatalf("non-song accepted: %s", tags)
		}
	}
}

func TestDownloadSendsCCMixterReferrerAndWritesAttribution(t *testing.T) {
	var gotReferer, gotAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Referer()
		gotAgent = r.UserAgent()
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(bytes.Repeat([]byte{0x42}, (1<<20)+1))
	}))
	defer server.Close()

	track := ccMixterTrack{ID: 7, Title: "Test", Artist: "Singer", PageURL: "https://ccmixter.org/files/singer/7", LicenseURL: "https://creativecommons.org/licenses/by/4.0/"}
	track.Files = append(track.Files, struct {
		DownloadURL string `json:"download_url"`
		RawSize     int64  `json:"file_rawsize"`
		Format      struct {
			MIME string `json:"mime_type"`
		} `json:"file_format_info"`
	}{DownloadURL: server.URL + "/song.mp3", RawSize: (1 << 20) + 1})
	track.Files[0].Format.MIME = "audio/mpeg"
	dir := t.TempDir()
	if err := download(context.Background(), server.Client(), track, dir); err != nil {
		t.Fatal(err)
	}
	if gotReferer != track.PageURL || !strings.Contains(gotAgent, "Mozilla") {
		t.Fatalf("missing hotlink headers: referer=%q agent=%q", gotReferer, gotAgent)
	}
	if _, err := os.Stat(filepath.Join(dir, "Test__Singer__ccmixter-7.license.json")); err != nil {
		t.Fatalf("attribution sidecar not written: %v", err)
	}
}
