package main

import "testing"

func TestHarnessRefusesToRunOutsideCheckMode(t *testing.T) {
	const local = "postgres://miniplay@127.0.0.1:55434/miniplay_portal"
	t.Setenv("MINIPLAY_PORTAL_CHECK", "")
	if err := requireCheckMode(local); err == nil {
		t.Fatal("the harness has no rate limits and must need an explicit opt-in")
	}
	t.Setenv("MINIPLAY_PORTAL_CHECK", "1")
	if err := requireCheckMode(local); err != nil {
		t.Fatal(err)
	}
	for _, remote := range []string{"postgres://u@db.example.com:5432/x", "postgres://u@10.0.0.5:5432/x", "postgres://u@localhost:5432/x"} {
		if err := requireCheckMode(remote); err == nil {
			t.Errorf("%s should be refused", remote)
		}
	}
}
