package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"miniplay/internal/config"
)

// upstreamTransport keeps enough idle connections to each service for
// concurrent requests. http.DefaultTransport keeps only two per host, so under
// load most proxied requests opened a new connection and left the old one in
// TIME_WAIT. In the local profile at 160 new games a second that held about
// 4,700 of the machine's 16,384 ephemeral ports.
func upstreamTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 1024
	transport.MaxIdleConnsPerHost = 256
	return transport
}

func proxy(raw string, transport http.RoundTripper) http.Handler {
	target, err := url.Parse(raw)
	if err != nil {
		log.Fatal(err)
	}
	handler := httputil.NewSingleHostReverseProxy(target)
	handler.Transport = transport
	return handler
}

func main() {
	upstream := upstreamTransport()
	session := proxy(config.Env("SESSION_URL", "http://127.0.0.1:8081"), upstream)
	score := proxy(config.Env("SCORE_URL", "http://127.0.0.1:8082"), upstream)
	leaderboard := proxy(config.Env("LEADERBOARD_URL", "http://127.0.0.1:8083"), upstream)
	webRoot := config.Env("WEB_DIST", "web/dist")
	if err := config.Serve(config.LocalAddr("8080"), newHandler(session, score, leaderboard, webRoot)); err != nil {
		log.Fatal(err)
	}
}

func newHandler(session, score, leaderboard http.Handler, webRoot string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/sessions", session)
	mux.Handle("/api/scores", score)
	mux.Handle("/api/leaderboards", leaderboard)
	mux.Handle("/api/boards/live", leaderboard)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	files := http.FileServer(http.Dir(webRoot))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// SPA fallback is limited to extensionless paths; missing assets return 404.
		name := filepath.Join(webRoot, strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/"))
		if _, err := os.Stat(name); err != nil && filepath.Ext(r.URL.Path) == "" {
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
	return mux
}
