// Package game defines the scoring rules shared with the TypeScript client.
// The server is authoritative: a submitted score is never trusted directly.
package game

import (
	"errors"
	"math"
)

type ID string

const (
	Rhythm          ID = "rhythm"
	Reflex          ID = "reflex"
	RunMilliseconds    = 10000
)

func ValidID(id ID) bool { return id == Rhythm || id == Reflex }

type Tap struct {
	MS int `json:"ms"`
	X  int `json:"x"`
	Y  int `json:"y"`
}

type Target struct{ MS, X, Y int }

func seedHash(seed string) uint32 {
	hash := uint32(2166136261)
	for i := 0; i < len(seed); i++ {
		hash = (hash ^ uint32(seed[i])) * 16777619
	}
	return hash
}

func Targets(id ID, seed string) ([]Target, error) {
	if !ValidID(id) {
		return nil, errors.New("unknown game")
	}
	state := seedHash(seed)
	if state == 0 {
		state = 1
	}
	next := func() uint32 {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		return state
	}
	count, interval, jitterMax := 18, 500, uint32(61)
	if id == Reflex {
		count, interval, jitterMax = 13, 700, 101
	}
	out := make([]Target, 0, count)
	for i := 0; i < count; i++ {
		jitter := int(next() % jitterMax)
		x, y := 500, 500
		if id == Reflex {
			x, y = 150+int(next()%701), 150+int(next()%701)
		}
		out = append(out, Target{MS: 450 + i*interval + jitter, X: x, Y: y})
	}
	return out, nil
}

func Replay(id ID, seed string, taps []Tap) (int, error) {
	if len(taps) > 100 {
		return 0, errors.New("too many taps")
	}
	targets, err := Targets(id, seed)
	if err != nil {
		return 0, err
	}
	used := make(map[int]bool, len(targets))
	previous := -1
	score := 0
	for _, tap := range taps {
		if tap.MS < previous || tap.MS < 0 || tap.MS > RunMilliseconds+250 || tap.X < 0 || tap.X > 1000 || tap.Y < 0 || tap.Y > 1000 {
			return 0, errors.New("invalid tap sequence")
		}
		previous = tap.MS
		window, radius, points := 110, 220, 10
		if id == Reflex {
			window, radius, points = 180, 110, 15
		}
		for i, target := range targets {
			if used[i] || abs(target.MS-tap.MS) > window || math.Hypot(float64(target.X-tap.X), float64(target.Y-tap.Y)) > float64(radius) {
				continue
			}
			used[i] = true
			score += points
			break
		}
	}
	return score, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// PlausibleDuration prevents zero-time and extended runs. It cannot prove a
// human played; bot-like cadence is evaluated separately from score validity.
func PlausibleDuration(durationMS int, issuedMS int64, nowMS int64) bool {
	return durationMS >= 9500 && durationMS <= 12000 && nowMS-issuedMS >= int64(durationMS-500) && nowMS-issuedMS <= 120000
}
