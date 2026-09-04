package status

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPollListenersReadsIcecastAdminCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "admin" || password != "secret" {
			t.Fatalf("unexpected auth: %q %q %v", user, password, ok)
		}
		_, _ = w.Write([]byte(`<icestats><source><listeners>1</listeners></source></icestats>`))
	}))
	defer server.Close()
	s := New(t.TempDir(), false, "/stream.mp3")
	s.SetIcecast(server.URL, "secret")
	count, ok := s.PollListeners()
	if !ok || count != 1 {
		t.Fatalf("PollListeners()=%d,%v want 1,true", count, ok)
	}
}

func TestPollListenersFallsBackToPublicStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status-json.xsl" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"icestats":{"source":{"listenurl":"http://localhost:7702/stream.mp3","listeners":2}}}`))
	}))
	defer server.Close()
	s := New(t.TempDir(), false, "/stream.mp3")
	s.SetIcecast(server.URL, "")
	count, ok := s.PollListeners()
	if !ok || count != 2 {
		t.Fatalf("PollListeners()=%d,%v want 2,true", count, ok)
	}
}
