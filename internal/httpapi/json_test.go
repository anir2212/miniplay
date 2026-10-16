package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"miniplay/internal/token"
)

type allowSession struct{}

func (allowSession) Allow(context.Context, string, int, float64) (bool, error) {
	return true, nil
}

func TestSessionAcceptsJSONMediaTypeWithCharset(t *testing.T) {
	signer, err := token.New(strings.Repeat("s", 32), func() time.Time { return time.Unix(1800000000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	handler := SessionHandler(signer, allowSession{})
	for _, test := range []struct {
		contentType string
		want        int
	}{
		{"application/json", 200},
		{"application/json; charset=utf-8", 200},
		{"application/jsonp", 400},
		{"application/json; charset", 400},
	} {
		t.Run(test.contentType, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"user":"alice","game":"rhythm"}`))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
