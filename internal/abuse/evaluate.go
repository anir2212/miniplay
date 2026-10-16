package abuse

import (
	"math/rand"
)

type Metrics struct {
	CleanRuns     int     `json:"cleanRuns"`
	InjectedBots  int     `json:"injectedBots"`
	TruePositive  int     `json:"truePositive"`
	FalsePositive int     `json:"falsePositive"`
	FalseNegative int     `json:"falseNegative"`
	Precision     float64 `json:"precision"`
	Recall        float64 `json:"recall"`
	Seed          int64   `json:"seed"`
}

// EvaluateInjected uses deterministic synthetic feature vectors. It measures
// only this test generator's separability, never real-world cheat detection.
func EvaluateInjected(seed int64) (Metrics, error) {
	random := rand.New(rand.NewSource(seed))
	clean := func() Vector {
		return Vector{
			0.35 + random.Float64()*0.4,
			9.85 + random.Float64()*0.4,
			22 + random.Float64()*85,
			0.08 + random.Float64()*0.35,
			5 + random.Float64()*12,
		}
	}
	bot := func() Vector {
		return Vector{
			0.92 + random.Float64()*0.08,
			9.99 + random.Float64()*0.03,
			random.Float64() * 3,
			random.Float64() * 0.018,
			16 + random.Float64()*3,
		}
	}
	training := make([]Vector, 0, 440)
	for i := 0; i < 400; i++ {
		training = append(training, clean())
	}
	for i := 0; i < 40; i++ {
		training = append(training, bot())
	}
	forest, err := Fit(training, 100, 64, seed)
	if err != nil {
		return Metrics{}, err
	}
	isolation := make([]float64, len(training))
	scoreBaseline := make([]float64, len(training))
	for i, vector := range training {
		isolation[i] = forest.Score(vector)
		scoreBaseline[i] = vector[0]
	}
	threshold := Quantile(isolation, 0.98)
	result := Metrics{CleanRuns: 200, InjectedBots: 80, Seed: seed}
	for i := 0; i < result.CleanRuns+result.InjectedBots; i++ {
		labelBot := i >= result.CleanRuns
		vector := clean()
		if labelBot {
			vector = bot()
		}
		flagged := len(Reasons(forest.Score(vector), threshold, ZScore(vector[0], scoreBaseline))) > 0
		if flagged && labelBot {
			result.TruePositive++
		}
		if flagged && !labelBot {
			result.FalsePositive++
		}
		if !flagged && labelBot {
			result.FalseNegative++
		}
	}
	if denominator := result.TruePositive + result.FalsePositive; denominator > 0 {
		result.Precision = float64(result.TruePositive) / float64(denominator)
	}
	if denominator := result.TruePositive + result.FalseNegative; denominator > 0 {
		result.Recall = float64(result.TruePositive) / float64(denominator)
	}
	return result, nil
}
