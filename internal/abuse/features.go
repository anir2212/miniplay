package abuse

import (
	"math"
	"miniplay/internal/game"
)

const dimensions = 5

type Vector [dimensions]float64

// Features are derived from the durable, server-validated score and tap stream.
// They help prioritize review; none proves a human or a bot played.
func Features(id game.ID, seed string, score, durationMS int, taps []game.Tap) (Vector, error) {
	targets, err := game.Targets(id, seed)
	if err != nil {
		return Vector{}, err
	}
	maxPoints := float64(len(targets) * 10)
	if id == game.Reflex {
		maxPoints = float64(len(targets) * 15)
	}
	timeError := 0.0
	for _, tap := range taps {
		best := float64(game.RunMilliseconds)
		for _, target := range targets {
			distance := math.Abs(float64(tap.MS - target.MS))
			if distance < best {
				best = distance
			}
		}
		timeError += best
	}
	if len(taps) > 0 {
		timeError /= float64(len(taps))
	}
	intervals := make([]float64, 0)
	for index := 1; index < len(taps); index++ {
		intervals = append(intervals, float64(taps[index].MS-taps[index-1].MS))
	}
	cadenceCV := 0.0
	if len(intervals) > 1 {
		mean := 0.0
		for _, interval := range intervals {
			mean += interval
		}
		mean /= float64(len(intervals))
		if mean > 0 {
			variance := 0.0
			for _, interval := range intervals {
				variance += math.Pow(interval-mean, 2)
			}
			cadenceCV = math.Sqrt(variance/float64(len(intervals))) / mean
		}
	}
	return Vector{float64(score) / maxPoints, float64(durationMS) / 1000, timeError, cadenceCV, float64(len(taps))}, nil
}
