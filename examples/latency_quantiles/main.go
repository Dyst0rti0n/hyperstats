// latency_quantiles demonstrates streaming quantile estimation for
// latency monitoring using both KLL and t-digest. KLL gives the best
// worst-case bound; t-digest gives the best tail accuracy. Comparing
// the two on the same stream illustrates the tradeoff.
//
// Usage: go run ./examples/latency_quantiles
package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/dystortion/hyperstats/kll"
	"github.com/dystortion/hyperstats/tdigest"
)

func main() {
	const N = 5_000_000

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Latency-style distribution: log-normal with a heavy upper tail.
	// μ=4, σ=0.6 → median ≈ 55ms, p99 ≈ 220ms, p99.9 ≈ 380ms.
	gen := func() float64 {
		return math.Exp(rng.NormFloat64()*0.6 + 4.0)
	}

	kllSketch := kll.New(200)    // ε_q ≈ 0.83% per query
	tdSketch := tdigest.New(100) // ε_p99 ≈ 0.05% empirically
	allValues := make([]float64, N)

	fmt.Printf("Simulating %d log-normal latency observations...\n", N)
	start := time.Now()
	for i := 0; i < N; i++ {
		v := gen()
		allValues[i] = v
		kllSketch.Add(v)
		tdSketch.Add(v)
	}
	elapsed := time.Since(start)
	fmt.Printf("Throughput: %.1f million obs/sec (both sketches in parallel)\n\n",
		float64(N)/elapsed.Seconds()/1e6)

	// Ground truth.
	sort.Float64s(allValues)

	probes := []float64{0.5, 0.9, 0.99, 0.999, 0.9999}
	fmt.Printf("%-8s  %-12s  %-12s  %-9s  %-12s  %-9s\n",
		"q", "true (ms)", "KLL (ms)", "KLL err%", "t-digest", "td err%")
	for _, q := range probes {
		idx := int(math.Floor(q * float64(N)))
		if idx >= N {
			idx = N - 1
		}
		truth := allValues[idx]

		kllVal := kllSketch.Quantile(q)
		tdVal := tdSketch.Quantile(q)

		kllErr := math.Abs(kllVal-truth) / truth * 100
		tdErr := math.Abs(tdVal-truth) / truth * 100

		fmt.Printf("p%-7.4f  %-12.2f  %-12.2f  %-9.4f  %-12.2f  %-9.4f\n",
			q*100, truth, kllVal, kllErr, tdVal, tdErr)
	}

	fmt.Printf("\nMemory:\n")
	fmt.Printf("  KLL:       %d bytes (k=200)\n", kllSketch.MemoryBytes())
	fmt.Printf("  t-digest:  %d bytes (δ=100, %d centroids)\n",
		tdSketch.MemoryBytes(), tdSketch.CentroidCount())

	// Demonstrate exact merge of KLL across shards.
	const shards = 4
	kllShards := make([]*kll.Sketch, shards)
	tdShards := make([]*tdigest.Sketch, shards)
	for s := range kllShards {
		kllShards[s] = kll.New(200)
		tdShards[s] = tdigest.New(100)
	}
	for i := 0; i < 100_000; i++ {
		v := gen()
		kllShards[i%shards].Add(v)
		tdShards[i%shards].Add(v)
	}
	for s := 1; s < shards; s++ {
		_ = kllShards[0].Merge(kllShards[s])
		_ = tdShards[0].Merge(tdShards[s])
	}
	fmt.Printf("\nAfter sharded merge across %d shards (100k obs total):\n", shards)
	fmt.Printf("  KLL p99:       %.2f ms\n", kllShards[0].Quantile(0.99))
	fmt.Printf("  t-digest p99:  %.2f ms\n", tdShards[0].Quantile(0.99))
}
