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

func proxy(raw string) http.Handler {
	target, err := url.Parse(raw)
	if err != nil {
		log.Fatal(err)
	}
	return httputil.NewSingleHostReverseProxy(target)
}

func main() {
	session := proxy(config.Env("SESSION_URL", "http://127.0.0.1:8081"))
	score := proxy(config.Env("SCORE_URL", "http://127.0.0.1:8082"))
	leaderboard := proxy(config.Env("LEADERBOARD_URL", "http://127.0.0.1:8083"))
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
