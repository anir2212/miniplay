package token

import (
	"strings"
	"testing"
	"time"

	"miniplay/internal/game"
)

func TestIssueVerifyAndExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	signer, err := New(strings.Repeat("x", 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	started, err := signer.Issue("alice", game.Rhythm)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.Verify(started.Token)
	if err != nil || claims.User != "alice" || claims.Seed != started.Seed {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err := signer.Verify(started.Token + "x"); err == nil {
		t.Fatal("tamper accepted")
	}
	now = now.Add(121 * time.Second)
	if _, err := signer.Verify(started.Token); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestRejectsWeakSecretAndInvalidUser(t *testing.T) {
	if _, err := New("short", nil); err == nil {
		t.Fatal("weak secret accepted")
	}
	signer, _ := New(strings.Repeat("x", 32), nil)
	if _, err := signer.Issue("bad user", game.Reflex); err == nil {
		t.Fatal("bad user accepted")
	}
}
