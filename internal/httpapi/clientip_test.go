package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"

	"miniplay/internal/token"
)

func TestClientIPTrustsOnlyProxyAddedForwardedEntries(t *testing.T) {
	cases := []struct {
		name, remote string
		forwarded    []string
		want         string
	}{
		{"direct client ignores its own header", "203.0.113.9:5000", []string{"198.51.100.1"}, "203.0.113.9"},
		{"loopback gateway without header", "127.0.0.1:5000", nil, "127.0.0.1"},
		{"loopback gateway forwards client", "127.0.0.1:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"forged left entry is skipped", "127.0.0.1:5000", []string{"198.51.100.1, 203.0.113.7"}, "203.0.113.7"},
		{"two trusted hops", "127.0.0.1:5000", []string{"203.0.113.7, 127.0.0.1"}, "203.0.113.7"},
		{"repeated header lines", "[::1]:5000", []string{"198.51.100.1", "203.0.113.7"}, "203.0.113.7"},
		{"malformed entry stops the walk", "127.0.0.1:5000", []string{"203.0.113.7, nonsense"}, "127.0.0.1"},
		{"mapped IPv4 peer", "[::ffff:127.0.0.1]:5000", []string{"203.0.113.7"}, "203.0.113.7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = c.remote
			for _, line := range c.forwarded {
				r.Header.Add("X-Forwarded-For", line)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 10.0.0.0/8, 192.0.2.5 ,::1/128,")
	if err != nil || len(got) != 3 || got[1].String() != "192.0.2.5/32" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := ParseTrustedProxies("10.0.0.0/8,not-an-ip"); err == nil {
		t.Fatal("expected an error for an invalid entry")
	}
}

type keyLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (l *keyLimiter) Allow(_ context.Context, key string, _ int, _ float64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return true, nil
}

// Two players behind the real gateway proxy must get separate session buckets.
func TestSessionLimitKeysOnClientBehindGatewayProxy(t *testing.T) {
	signer, err := token.New(strings.Repeat("s", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	limiter := &keyLimiter{}
	service := httptest.NewServer(SessionHandler(signer, limiter))
	defer service.Close()
	target, _ := url.Parse(service.URL)
	gateway := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	defer gateway.Close()
	for _, client := range []string{"203.0.113.7", "203.0.113.8"} {
		request, _ := http.NewRequest(http.MethodPost, gateway.URL+"/api/sessions",
			strings.NewReader(`{"user":"player1","game":"rhythm"}`))
		request.Header.Set("Content-Type", "application/json")
		// Stands in for an upstream proxy in front of the gateway, both on loopback.
		request.Header.Set("X-Forwarded-For", client)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status %d", response.StatusCode)
		}
	}
	want := []string{"session-ip:203.0.113.7", "session-ip:203.0.113.8"}
	if strings.Join(limiter.keys, " ") != strings.Join(want, " ") {
		t.Fatalf("limiter keys %v, want %v", limiter.keys, want)
	}
}
