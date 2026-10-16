// Package token signs short-lived game run claims with an HMAC shared by the
// session and score services. The run ID is random and doubles as an idempotency key.
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"miniplay/internal/game"
)

var raw = base64.RawURLEncoding

type Claims struct {
	RunID     string  `json:"runId"`
	User      string  `json:"user"`
	Game      game.ID `json:"game"`
	Seed      string  `json:"seed"`
	IssuedAt  int64   `json:"issuedAt"`
	ExpiresAt int64   `json:"expiresAt"`
}

type Start struct {
	Token     string `json:"token"`
	RunID     string `json:"runId"`
	Seed      string `json:"seed"`
	ExpiresAt int64  `json:"expiresAt"`
}

type Signer struct {
	secret []byte
	now    func() time.Time
}

func New(secret string, clock func() time.Time) (*Signer, error) {
	if len(secret) < 32 {
		return nil, errors.New("MINIPLAY_HMAC_SECRET must have at least 32 characters")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Signer{secret: []byte(secret), now: clock}, nil
}

func ValidUser(user string) bool {
	if len(user) < 1 || len(user) > 40 {
		return false
	}
	for _, c := range user {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (s *Signer) Issue(user string, id game.ID) (Start, error) {
	if !ValidUser(user) || !game.ValidID(id) {
		return Start{}, errors.New("invalid user or game")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return Start{}, err
	}
	runID := raw.EncodeToString(nonce)
	now := s.now().UnixMilli()
	claims := Claims{RunID: runID, User: user, Game: id, Seed: runID, IssuedAt: now, ExpiresAt: now + 120000}
	payload, err := json.Marshal(claims)
	if err != nil {
		return Start{}, err
	}
	encoded := raw.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	return Start{Token: encoded + "." + raw.EncodeToString(mac.Sum(nil)), RunID: runID, Seed: runID, ExpiresAt: claims.ExpiresAt}, nil
}

func (s *Signer) Verify(value string) (Claims, error) {
	var claims Claims
	if len(value) > 2048 {
		return claims, errors.New("invalid token")
	}
	pieces := strings.Split(value, ".")
	if len(pieces) != 2 || pieces[0] == "" || pieces[1] == "" {
		return claims, errors.New("invalid token")
	}
	provided, err := raw.DecodeString(pieces[1])
	if err != nil {
		return claims, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(pieces[0]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return claims, errors.New("invalid signature")
	}
	payload, err := raw.DecodeString(pieces[0])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return Claims{}, errors.New("invalid claims")
	}
	now := s.now().UnixMilli()
	if !ValidUser(claims.User) || !game.ValidID(claims.Game) || len(claims.RunID) < 16 || claims.Seed != claims.RunID || claims.IssuedAt > now+5000 || claims.ExpiresAt <= now || claims.ExpiresAt-claims.IssuedAt != 120000 {
		return Claims{}, errors.New("expired or invalid claims")
	}
	return claims, nil
}
