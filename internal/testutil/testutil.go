// Package testutil holds shared helpers for hyperstats property tests.
// It is internal so it cannot be imported by downstream packages.
package testutil

import (
	"math"
	"math/rand"
	"sort"
)

// RandomBytes generates a deterministic stream of fixed-length random
// byte slices for sketch-update tests. Fixed length avoids any
// length-induced bias in MurmurHash3's tail handling.
func RandomBytes(seed int64, n, length int) [][]byte {
	rng := rand.New(rand.NewSource(seed))
	out := make([][]byte, n)
	for i := range out {
		buf := make([]byte, length)
		_, _ = rng.Read(buf)
		out[i] = buf
	}
	return out
}

// RandomFloats generates n uniformly-random floats in [0, 1).
func RandomFloats(seed int64, n int) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = rng.Float64()
	}
	return out
}

// SortedFloats returns a new slice with the input values sorted ascending.
// The input is not modified.
func SortedFloats(values []float64) []float64 {
	out := make([]float64, len(values))
	copy(out, values)
	sort.Float64s(out)
	return out
}

// TrueQuantile returns the exact q-quantile of an already-sorted slice.
// Uses the same indexing rule as the property tests: floor(q * len).
func TrueQuantile(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return math.NaN()
	}
	idx := int(math.Floor(q * float64(len(sortedValues))))
	if idx >= len(sortedValues) {
		idx = len(sortedValues) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return sortedValues[idx]
}

// RMSE returns the root-mean-square error of a slice of relative errors.
func RMSE(errors []float64) float64 {
	if len(errors) == 0 {
		return 0
	}
	var sumSq float64
	for _, e := range errors {
		sumSq += e * e
	}
	return math.Sqrt(sumSq / float64(len(errors)))
}

// Mean returns the arithmetic mean of a slice of floats.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// PercentileOfSorted returns the q-percentile of an already-sorted slice
// using the floor-of-index rule (matches the property tests' true-rank
// computation).
func PercentileOfSorted(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return math.NaN()
	}
	idx := int(math.Min(float64(len(sortedValues)-1), float64(len(sortedValues))*q))
	return sortedValues[idx]
}
