// Command load-probe runs a timed load profile against the public gateway.
// New runs start at a fixed rate. Each run opens its player's live board,
// starts a session, waits out the real ten-second game window, submits the
// replayed score and times how long the player's rank takes to reach the
// socket. Leaderboard reads and idle watchers add read and fan-out load.
//
// Every run uses its own user and a synthetic client address from the
// 198.18.0.0/15 benchmark range, sent as X-Forwarded-For. The services believe
// it only because the probe and gateway both connect from loopback, which is
// trusted (see internal/httpapi/clientip.go); it stands in for distinct players.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"miniplay/internal/game"
	"miniplay/internal/token"
)

type recorder struct {
	mu      sync.Mutex
	latency map[string][]float64
	status  map[string]map[int]int
	errors  map[string]int
}

func newRecorder() *recorder {
	return &recorder{latency: map[string][]float64{}, status: map[string]map[int]int{}, errors: map[string]int{}}
}

func (r *recorder) add(name string, elapsed time.Duration, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latency[name] = append(r.latency[name], float64(elapsed.Microseconds())/1000)
	if status == 0 { // not an HTTP measurement
		return
	}
	if r.status[name] == nil {
		r.status[name] = map[int]int{}
	}
	r.status[name][status]++
}

func (r *recorder) fail(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors[name]++
}

type summary struct {
	Count  int         `json:"count"`
	P50MS  float64     `json:"p50Ms"`
	P95MS  float64     `json:"p95Ms"`
	P99MS  float64     `json:"p99Ms"`
	MaxMS  float64     `json:"maxMs"`
	Status map[int]int `json:"status,omitempty"`
	Errors int         `json:"errors"`
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(p*float64(len(sorted)-1) + 0.5)
	return sorted[index]
}

func (r *recorder) summaries() map[string]summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]summary{}
	names := map[string]bool{}
	for name := range r.latency {
		names[name] = true
	}
	for name := range r.errors {
		names[name] = true
	}
	for name := range names {
		values := append([]float64(nil), r.latency[name]...)
		sort.Float64s(values)
		s := summary{Count: len(values), Status: r.status[name], Errors: r.errors[name]}
		if len(values) > 0 {
			s.P50MS, s.P95MS, s.P99MS = percentile(values, .5), percentile(values, .95), percentile(values, .99)
			s.MaxMS = values[len(values)-1]
		}
		out[name] = s
	}
	return out
}

type probe struct {
	base   string
	client *http.Client
	rec    *recorder
}

func syntheticIP(index int) string {
	return fmt.Sprintf("198.%d.%d.%d", 18+index/(256*254), index/254%256, index%254+1)
}

func (p probe) post(name, path, ip string, value any) (int, []byte, error) {
	body, _ := json.Marshal(value)
	request, _ := http.NewRequest(http.MethodPost, p.base+path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", ip)
	started := time.Now()
	response, err := p.client.Do(request)
	if err != nil {
		p.rec.fail(name)
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	p.rec.add(name, time.Since(started), response.StatusCode)
	return response.StatusCode, raw, nil
}

func (p probe) watch(ctx context.Context, user string) (*websocket.Conn, error) {
	u, _ := url.Parse(p.base)
	origin := u.String()
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.Path = "/api/boards/live"
	query := url.Values{"game": {"rhythm"}, "period": {"all"}}
	if user != "" {
		query.Set("user", user)
	}
	u.RawQuery = query.Encode()
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {origin}}})
	if err == nil {
		conn.SetReadLimit(1 << 20)
	}
	return conn, err
}

// play is one player's run; the socket stays open from before the session
// starts until the player's rank appears or the frame timeout passes.
func (p probe) play(index int, prefix string, hits int, frameTimeout time.Duration) {
	user, ip := fmt.Sprintf("%s%06d", prefix, index), syntheticIP(index)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	conn, err := p.watch(ctx, user)
	if err != nil {
		p.rec.fail("live_connect")
		return
	}
	defer conn.CloseNow()
	ranked := make(chan time.Time, 1)
	go func() {
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var frame struct {
				Board struct {
					MyRank *int64 `json:"myRank"`
				} `json:"board"`
			}
			if json.Unmarshal(payload, &frame) == nil && frame.Board.MyRank != nil {
				select {
				case ranked <- time.Now():
				default:
				}
				return
			}
		}
	}()
	status, raw, err := p.post("session", "/api/sessions", ip, map[string]string{"user": user, "game": "rhythm"})
	if err != nil || status != http.StatusOK {
		return
	}
	var started token.Start
	if json.Unmarshal(raw, &started) != nil {
		p.rec.fail("session_decode")
		return
	}
	targets, err := game.Targets(game.Rhythm, started.Seed)
	if err != nil || hits > len(targets) {
		p.rec.fail("plan")
		return
	}
	taps := make([]game.Tap, 0, hits)
	for _, target := range targets[:hits] {
		taps = append(taps, game.Tap{MS: target.MS, X: target.X, Y: target.Y})
	}
	score, _ := game.Replay(game.Rhythm, started.Seed, taps)
	time.Sleep(10200 * time.Millisecond) // real game window; see game.PlausibleDuration
	submission := map[string]any{"token": started.Token, "events": taps, "score": score, "durationMs": 10000}
	submitted := time.Now()
	status, _, err = p.post("score", "/api/scores", ip, submission)
	if err != nil || status >= 300 {
		return
	}
	select {
	case at := <-ranked:
		p.rec.add("live_rank_frame", at.Sub(submitted), 0)
	case <-time.After(frameTimeout):
		p.rec.fail("live_rank_frame")
	}
}

func (p probe) idleWatcher(ctx context.Context, frames *int64, mu *sync.Mutex) {
	conn, err := p.watch(ctx, "")
	if err != nil {
		p.rec.fail("watcher_connect")
		return
	}
	defer conn.CloseNow()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() == nil {
				p.rec.fail("watcher_dropped")
			}
			return
		}
		mu.Lock()
		*frames++
		mu.Unlock()
	}
}

// limitCheck sends a burst of session starts from one synthetic client and
// counts how many the per-client limit admits.
func (p probe) limitCheck(count int) map[int]int {
	ip := "198.19.255.254"
	got := map[int]int{}
	for i := 0; i < count; i++ {
		status, _, _ := p.post("limit_check", "/api/sessions", ip, map[string]string{"user": "lplimit", "game": "rhythm"})
		got[status]++
	}
	return got
}

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:8080", "gateway base URL")
	rate := flag.Float64("rate", 5, "new runs started per second")
	duration := flag.Duration("duration", 60*time.Second, "how long new runs keep starting")
	hits := flag.Int("hits", 12, "targets each run hits")
	watchers := flag.Int("watchers", 0, "idle live-board sockets held for the whole profile")
	boardRate := flag.Float64("board-rate", 0, "leaderboard GETs per second")
	frameTimeout := flag.Duration("frame-timeout", 15*time.Second, "wait for the player's rank frame after scoring")
	limit := flag.Int("limit-check", 0, "if above 0, first send this many session starts from one client")
	prefix := flag.String("prefix", "lp", "user ID prefix for this profile")
	flag.Parse()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost = 2000, 2000
	p := probe{base: strings.TrimRight(*gateway, "/"), client: &http.Client{Timeout: 10 * time.Second, Transport: transport}, rec: newRecorder()}

	report := map[string]any{"config": map[string]any{
		"gateway": *gateway, "ratePerSecond": *rate, "durationSeconds": duration.Seconds(), "hits": *hits,
		"watchers": *watchers, "boardRatePerSecond": *boardRate, "frameTimeoutSeconds": frameTimeout.Seconds(),
		"goVersion": runtime.Version(), "cpus": runtime.NumCPU(),
	}}
	if *limit > 0 {
		report["limitCheck"] = p.limitCheck(*limit)
	}

	ctx, stop := context.WithCancel(context.Background())
	var watcherFrames int64
	var frameMu sync.Mutex
	var background sync.WaitGroup
	for i := 0; i < *watchers; i++ {
		background.Add(1)
		go func() { defer background.Done(); p.idleWatcher(ctx, &watcherFrames, &frameMu) }()
	}
	if *boardRate > 0 {
		background.Add(1)
		go func() {
			defer background.Done()
			ticker := time.NewTicker(time.Duration(float64(time.Second) / *boardRate))
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					go func() {
						request, _ := http.NewRequest(http.MethodGet, p.base+"/api/leaderboards?game=rhythm&period=all", nil)
						started := time.Now()
						response, err := p.client.Do(request)
						if err != nil {
							p.rec.fail("leaderboard")
							return
						}
						_, _ = io.Copy(io.Discard, response.Body)
						response.Body.Close()
						p.rec.add("leaderboard", time.Since(started), response.StatusCode)
					}()
				}
			}
		}()
	}

	began := time.Now()
	var runs sync.WaitGroup
	interval := time.Duration(float64(time.Second) / *rate)
	scheduled := 0
	for next := began; next.Before(began.Add(*duration)); next = next.Add(interval) {
		time.Sleep(time.Until(next))
		runs.Add(1)
		go func(index int) { defer runs.Done(); p.play(index, *prefix, *hits, *frameTimeout) }(scheduled)
		scheduled++
	}
	runs.Wait()
	stop()
	background.Wait()

	report["scheduledRuns"] = scheduled
	report["elapsedSeconds"] = time.Since(began).Seconds()
	report["watcherFrames"] = watcherFrames
	report["results"] = p.rec.summaries()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		log.Fatal(err)
	}
}
