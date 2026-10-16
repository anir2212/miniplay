package httpapi

import (
	"context"
	"net"
	"net/http"

	"miniplay/internal/game"
	"miniplay/internal/token"
)

type Limiter interface {
	Allow(context.Context, string, int, float64) (bool, error)
}

func SessionHandler(signer *token.Signer, limiter Limiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			User string  `json:"user"`
			Game game.ID `json:"game"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, 400, err.Error())
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		allowed, err := limiter.Allow(r.Context(), "session-ip:"+ip, 30, 0.5)
		if err != nil {
			failure(w, 503, "rate limiter unavailable")
			return
		}
		if !allowed {
			failure(w, 429, "rate limit exceeded")
			return
		}
		started, err := signer.Issue(request.User, request.Game)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		respond(w, 200, started)
	})
	return mux
}
