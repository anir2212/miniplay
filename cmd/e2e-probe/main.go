// Command e2e-probe drives the public gateway contract against running
// services. It is a local verification tool, not a load generator: each run
// waits for the real ten-second game window because the score service checks
// elapsed time against the signed issue time.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"miniplay/internal/game"
	"miniplay/internal/token"
)

type frame struct {
	Type  string `json:"type"`
	Game  string `json:"game"`
	Board struct {
		Leaders []struct {
			User  string `json:"user"`
			Score int    `json:"score"`
		} `json:"leaders"`
		MyRank *int64 `json:"myRank"`
	} `json:"board"`
}

type reply struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// plannedTaps returns one exact tap for each of the first hits targets and the
// score the server replay should compute for them.
func plannedTaps(id game.ID, seed string, hits int) ([]game.Tap, int, error) {
	targets, err := game.Targets(id, seed)
	if err != nil {
		return nil, 0, err
	}
	if hits < 0 || hits > len(targets) {
		return nil, 0, fmt.Errorf("hits must be between 0 and %d", len(targets))
	}
	taps := make([]game.Tap, 0, hits)
	for _, target := range targets[:hits] {
		taps = append(taps, game.Tap{MS: target.MS, X: target.X, Y: target.Y})
	}
	score, err := game.Replay(id, seed, taps)
	return taps, score, err
}

// tamperUser changes the signed user claim while keeping the original MAC.
func tamperUser(value, user string) (string, error) {
	pieces := strings.Split(value, ".")
	if len(pieces) != 2 {
		return "", errors.New("token must have two parts")
	}
	payload, err := base64.RawURLEncoding.DecodeString(pieces[0])
	if err != nil {
		return "", err
	}
	var claims token.Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	claims.User = user
	changed, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(changed) + "." + pieces[1], nil
}

// expiredToken signs a correctly formed token whose 120-second lifetime ended
// before now. It needs the same private secret as the running services.
func expiredToken(secret, user string, id game.ID, now time.Time) (token.Start, error) {
	signer, err := token.New(secret, func() time.Time { return now.Add(-3 * time.Minute) })
	if err != nil {
		return token.Start{}, err
	}
	return signer.Issue(user, id)
}

func boardScore(f frame, user string) (int, bool) {
	for _, leader := range f.Board.Leaders {
		if leader.User == user {
			return leader.Score, true
		}
	}
	return 0, false
}

type client struct {
	base string
	http *http.Client
}

func (c client) post(path string, value any, contentType string) (reply, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return reply{}, err
	}
	request, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return reply{}, err
	}
	request.Header.Set("Content-Type", contentType)
	return c.do(request)
}

func (c client) get(path string) (reply, error) {
	request, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return reply{}, err
	}
	return c.do(request)
}

func (c client) do(request *http.Request) (reply, error) {
	response, err := c.http.Do(request)
	if err != nil {
		return reply{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return reply{}, err
	}
	if !json.Valid(raw) {
		raw, _ = json.Marshal(strings.TrimSpace(string(raw)))
	}
	return reply{Status: response.StatusCode, Body: raw}, nil
}

func (c client) start(user string, id game.ID) (token.Start, reply, error) {
	result, err := c.post("/api/sessions", map[string]string{"user": user, "game": string(id)}, "application/json")
	if err != nil {
		return token.Start{}, result, err
	}
	var started token.Start
	if result.Status == http.StatusOK {
		err = json.Unmarshal(result.Body, &started)
	}
	return started, result, err
}

// watch opens the live board through the gateway with a browser-style Origin
// header and forwards decoded frames until the context ends.
func (c client) watch(ctx context.Context, id game.ID, user string) (<-chan frame, <-chan error, error) {
	u, err := url.Parse(c.base)
	if err != nil {
		return nil, nil, err
	}
	origin := u.String()
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.Path = "/api/boards/live"
	u.RawQuery = url.Values{"game": {string(id)}, "period": {"all"}, "user": {user}}.Encode()
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Origin": {origin}}})
	if err != nil {
		return nil, nil, err
	}
	frames, failures := make(chan frame, 16), make(chan error, 1)
	go func() {
		defer conn.CloseNow()
		defer close(frames)
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				failures <- err
				return
			}
			var message frame
			if json.Unmarshal(payload, &message) == nil {
				frames <- message
			}
		}
	}()
	return frames, failures, nil
}

type playResult struct {
	User              string `json:"user"`
	Game              string `json:"game"`
	SocketHeldMS      int64  `json:"socketHeldMs"`
	InitialFrameScore *int   `json:"initialFrameScore"`
	Session           reply  `json:"session"`
	RunID             string `json:"runId"`
	ExpectedScore     int    `json:"expectedScore"`
	Score             reply  `json:"score"`
	Duplicate         reply  `json:"duplicate"`
	FrameScore        *int   `json:"frameScore"`
	FrameAfterMS      int64  `json:"frameAfterScoreMs"`
	FrameLeaders      int    `json:"frameLeaders"`
	SocketError       string `json:"socketError,omitempty"`
}

func play(c client, user string, id game.ID, hits int, hold, frameTimeout time.Duration) (playResult, error) {
	result := playResult{User: user, Game: string(id)}
	ctx, cancel := context.WithTimeout(context.Background(), hold+frameTimeout+time.Minute)
	defer cancel()
	frames, failures, err := c.watch(ctx, id, user)
	if err != nil {
		return result, fmt.Errorf("live board: %w", err)
	}
	opened := time.Now()
	select {
	case first, ok := <-frames:
		if !ok {
			return result, errors.New("live board closed before initial snapshot")
		}
		if score, found := boardScore(first, user); found {
			result.InitialFrameScore = &score
		}
	case <-time.After(10 * time.Second):
		return result, errors.New("no initial live frame")
	}
	time.Sleep(hold)
	result.SocketHeldMS = time.Since(opened).Milliseconds()
	started, session, err := c.start(user, id)
	result.Session = session
	if err != nil || session.Status != http.StatusOK {
		return result, fmt.Errorf("session status=%d err=%v", session.Status, err)
	}
	result.RunID = started.RunID
	taps, expected, err := plannedTaps(id, started.Seed, hits)
	if err != nil {
		return result, err
	}
	result.ExpectedScore = expected
	time.Sleep(10200 * time.Millisecond) // real game window; see game.PlausibleDuration
	submission := map[string]any{"token": started.Token, "events": taps, "score": expected, "durationMs": 10000}
	if result.Score, err = c.post("/api/scores", submission, "application/json"); err != nil {
		return result, err
	}
	submitted := time.Now()
	if result.Duplicate, err = c.post("/api/scores", submission, "application/json; charset=utf-8"); err != nil {
		return result, err
	}
	deadline := time.After(frameTimeout)
	for {
		select {
		case message, ok := <-frames:
			if !ok {
				select {
				case failure := <-failures:
					result.SocketError = failure.Error()
				default:
				}
				return result, nil
			}
			if score, found := boardScore(message, user); found && score >= expected {
				result.FrameScore, result.FrameAfterMS, result.FrameLeaders = &score, time.Since(submitted).Milliseconds(), len(message.Board.Leaders)
				return result, nil
			}
		case <-deadline:
			result.SocketError = "no matching frame before timeout"
			return result, nil
		}
	}
}

type abuseCase struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Body   any    `json:"body"`
}

func abuse(c client, user string, secret string) ([]abuseCase, error) {
	results := make([]abuseCase, 0, 12)
	add := func(name string, r reply, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		var body any
		_ = json.Unmarshal(r.Body, &body)
		results = append(results, abuseCase{Name: name, Status: r.Status, Body: body})
		return nil
	}
	// Start every token-bearing run first so one shared real-time wait covers them.
	names := []string{"tampered-user-claim", "invalid-tap-coordinates", "claimed-score-mismatch", "short-duration"}
	starts := make(map[string]token.Start, len(names))
	for _, name := range names {
		started, r, err := c.start(user, game.Rhythm)
		if err != nil || r.Status != http.StatusOK {
			return results, fmt.Errorf("start %s: status=%d err=%v", name, r.Status, err)
		}
		starts[name] = started
	}
	r, err := c.post("/api/sessions", map[string]string{"user": "bad user!", "game": "rhythm"}, "application/json")
	if err := add("session-invalid-user", r, err); err != nil {
		return results, err
	}
	r, err = c.post("/api/sessions", map[string]string{"user": user, "game": "rhythm"}, "text/plain")
	if err := add("session-wrong-media-type", r, err); err != nil {
		return results, err
	}
	r, err = c.get("/api/unknown")
	if err := add("gateway-unknown-api", r, err); err != nil {
		return results, err
	}
	r, err = c.get("/api/leaderboards?game=rhythm&period=hourly")
	if err := add("leaderboard-invalid-period", r, err); err != nil {
		return results, err
	}
	if secret != "" {
		expired, err := expiredToken(secret, user, game.Rhythm, time.Now())
		if err != nil {
			return results, err
		}
		r, err = c.post("/api/scores", map[string]any{"token": expired.Token, "events": []game.Tap{}, "score": 0, "durationMs": 10000}, "application/json")
		if err := add("expired-token", r, err); err != nil {
			return results, err
		}
	}
	time.Sleep(10200 * time.Millisecond)
	tampered, err := tamperUser(starts["tampered-user-claim"].Token, user+"x")
	if err != nil {
		return results, err
	}
	r, err = c.post("/api/scores", map[string]any{"token": tampered, "events": []game.Tap{}, "score": 0, "durationMs": 10000}, "application/json")
	if err := add("tampered-user-claim", r, err); err != nil {
		return results, err
	}
	invalid := map[string]any{"token": starts["invalid-tap-coordinates"].Token, "events": []game.Tap{{MS: 500, X: 2000, Y: 500}}, "score": 0, "durationMs": 10000}
	r, err = c.post("/api/scores", invalid, "application/json")
	if err := add("invalid-tap-coordinates", r, err); err != nil {
		return results, err
	}
	r, err = c.post("/api/scores", invalid, "application/json")
	if err := add("rejected-run-resubmitted", r, err); err != nil {
		return results, err
	}
	taps, score, err := plannedTaps(game.Rhythm, starts["claimed-score-mismatch"].Seed, 3)
	if err != nil {
		return results, err
	}
	r, err = c.post("/api/scores", map[string]any{"token": starts["claimed-score-mismatch"].Token, "events": taps, "score": score + 100, "durationMs": 10000}, "application/json")
	if err := add("claimed-score-mismatch", r, err); err != nil {
		return results, err
	}
	r, err = c.post("/api/scores", map[string]any{"token": starts["short-duration"].Token, "events": []game.Tap{}, "score": 0, "durationMs": 2000}, "application/json")
	if err := add("short-duration", r, err); err != nil {
		return results, err
	}
	return results, nil
}

// newHTTPClient returns the probe's client. With a CA file it trusts only
// that CA, so an https gateway with a locally issued certificate can be probed
// without adding the CA to the system store.
func newHTTPClient(caFile string) (*http.Client, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	if caFile == "" {
		return client, nil
	}
	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%s holds no PEM certificate", caFile)
	}
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	return client, nil
}

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:8080", "gateway base URL")
	mode := flag.String("mode", "play", "play or abuse")
	user := flag.String("user", "probe1", "demo user ID")
	id := flag.String("game", "rhythm", "rhythm or reflex")
	hits := flag.Int("hits", 18, "targets to hit in play mode")
	hold := flag.Duration("hold", 0, "keep the live socket open this long before starting the run")
	frameTimeout := flag.Duration("frame-timeout", 20*time.Second, "time to wait for the live frame after scoring")
	caFile := flag.String("ca", "", "PEM CA certificate to trust, instead of the system store, for an https gateway")
	flag.Parse()
	httpClient, err := newHTTPClient(*caFile)
	if err != nil {
		log.Fatal(err)
	}
	c := client{base: strings.TrimRight(*gateway, "/"), http: httpClient}
	var output any
	switch *mode {
	case "play":
		output, err = play(c, *user, game.ID(*id), *hits, *hold, *frameTimeout)
	case "abuse":
		output, err = abuse(c, *user, os.Getenv("MINIPLAY_HMAC_SECRET"))
	default:
		err = errors.New("mode must be play or abuse")
	}
	if output != nil {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(output)
	}
	if err != nil {
		log.Fatal(err)
	}
}
