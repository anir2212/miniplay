package abuse

import (
	"errors"
	"math"
	"math/rand"
	"sort"
)

type node struct {
	feature     int
	split       float64
	left, right *node
	size        int
}
type Forest struct {
	roots      []*node
	sampleSize int
}

// Fit grows random isolation trees on reproducible samples. It requires enough
// historical runs to separate sparse anomalies from ordinary variance.
func Fit(data []Vector, treeCount, sampleSize int, seed int64) (*Forest, error) {
	if len(data) < 16 || treeCount < 1 || sampleSize < 2 {
		return nil, errors.New("insufficient isolation-forest data or parameters")
	}
	for _, row := range data {
		for _, value := range row {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, errors.New("nonfinite feature")
			}
		}
	}
	if sampleSize > len(data) {
		sampleSize = len(data)
	}
	generator := rand.New(rand.NewSource(seed))
	maxDepth := int(math.Ceil(math.Log2(float64(sampleSize))))
	forest := &Forest{sampleSize: sampleSize, roots: make([]*node, 0, treeCount)}
	for i := 0; i < treeCount; i++ {
		indexes := generator.Perm(len(data))[:sampleSize]
		sample := make([]Vector, sampleSize)
		for j, index := range indexes {
			sample[j] = data[index]
		}
		forest.roots = append(forest.roots, build(sample, 0, maxDepth, generator))
	}
	return forest, nil
}

func build(data []Vector, depth, maxDepth int, generator *rand.Rand) *node {
	current := &node{size: len(data)}
	if len(data) < 2 || depth >= maxDepth {
		return current
	}
	start := generator.Intn(dimensions)
	for attempt := 0; attempt < dimensions; attempt++ {
		feature := (start + attempt) % dimensions
		minValue, maxValue := data[0][feature], data[0][feature]
		for _, row := range data[1:] {
			if row[feature] < minValue {
				minValue = row[feature]
			}
			if row[feature] > maxValue {
				maxValue = row[feature]
			}
		}
		if minValue == maxValue {
			continue
		}
		split := minValue + generator.Float64()*(maxValue-minValue)
		left, right := make([]Vector, 0), make([]Vector, 0)
		for _, row := range data {
			if row[feature] < split {
				left = append(left, row)
			} else {
				right = append(right, row)
			}
		}
		if len(left) == 0 || len(right) == 0 {
			continue
		}
		current.feature, current.split = feature, split
		current.left = build(left, depth+1, maxDepth, generator)
		current.right = build(right, depth+1, maxDepth, generator)
		return current
	}
	return current
}

func averagePath(size int) float64 {
	if size <= 1 {
		return 0
	}
	if size == 2 {
		return 1
	}
	return 2*(math.Log(float64(size-1))+0.5772156649) - 2*float64(size-1)/float64(size)
}
func path(root *node, row Vector, depth int) float64 {
	if root.left == nil || root.right == nil {
		return float64(depth) + averagePath(root.size)
	}
	if row[root.feature] < root.split {
		return path(root.left, row, depth+1)
	}
	return path(root.right, row, depth+1)
}
func (forest *Forest) Score(row Vector) float64 {
	total := 0.0
	for _, root := range forest.roots {
		total += path(root, row, 0)
	}
	return math.Pow(2, -(total/float64(len(forest.roots)))/averagePath(forest.sampleSize))
}

func Quantile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	if q <= 0 {
		return copied[0]
	}
	if q > 1 {
		q = 1
	}
	return copied[int(math.Ceil(q*float64(len(copied))))-1]
}

func ZScore(value float64, baseline []float64) float64 {
	if len(baseline) < 30 {
		return 0
	}
	mean := 0.0
	for _, point := range baseline {
		mean += point
	}
	mean /= float64(len(baseline))
	variance := 0.0
	for _, point := range baseline {
		variance += math.Pow(point-mean, 2)
	}
	variance /= float64(len(baseline))
	if variance == 0 {
		return 0
	}
	return (value - mean) / math.Sqrt(variance)
}
