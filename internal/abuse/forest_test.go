package abuse

import (
	"math"
	"math/rand"
	"testing"
)

func TestIsolationForestSeparatesSparseExtreme(t *testing.T) {
	random := rand.New(rand.NewSource(11))
	rows := make([]Vector, 100)
	for i := range rows {
		rows[i] = Vector{0.4 + random.Float64()*0.1, 10, 40 + random.Float64()*30, 0.1 + random.Float64()*0.1, 10 + random.Float64()*5}
	}
	forest, err := Fit(rows, 64, 64, 17)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := forest.Score(rows[0])
	extreme := forest.Score(Vector{1, 10, 0, 0, 30})
	if extreme <= ordinary {
		t.Fatalf("extreme=%f ordinary=%f", extreme, ordinary)
	}
	again, _ := Fit(rows, 64, 64, 17)
	if math.Abs(again.Score(rows[0])-ordinary) > 1e-12 {
		t.Fatal("nondeterministic forest")
	}
}

func TestZScoreNeedsBaseline(t *testing.T) {
	if ZScore(100, []float64{1, 2, 3}) != 0 {
		t.Fatal("undersized baseline should not flag")
	}
	if Quantile([]float64{3, 1, 2}, 0.5) != 2 {
		t.Fatal("median quantile")
	}
}
