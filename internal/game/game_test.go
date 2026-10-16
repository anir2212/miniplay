package game

import "testing"

func TestDeterministicTargetsAndReplay(t *testing.T) {
	a, err := Targets(Reflex, "sample")
	if err != nil || len(a) != 13 {
		t.Fatalf("targets: %v %d", err, len(a))
	}
	b, _ := Targets(Reflex, "sample")
	if a[0] != b[0] {
		t.Fatal("target stream changed")
	}
	taps := []Tap{{MS: a[0].MS, X: a[0].X, Y: a[0].Y}, {MS: a[0].MS, X: a[0].X, Y: a[0].Y}}
	score, err := Replay(Reflex, "sample", taps)
	if err != nil || score != 15 {
		t.Fatalf("replay: score=%d err=%v", score, err)
	}
}

func TestInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		taps []Tap
	}{
		{"negative time", []Tap{{MS: -1, X: 500, Y: 500}}},
		{"out of order", []Tap{{MS: 1000, X: 500, Y: 500}, {MS: 999, X: 500, Y: 500}}},
		{"invalid coordinate", []Tap{{MS: 500, X: 1001, Y: 500}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Replay(Rhythm, "seed", tc.taps); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	if PlausibleDuration(100, 0, 10000) {
		t.Fatal("short run accepted")
	}
}
