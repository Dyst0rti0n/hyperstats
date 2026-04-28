// unique_visitors counts the approximate number of unique visitors to a
// website using HyperLogLog. Demonstrates the typical pattern: stream
// data in, periodically estimate, optionally serialise for later merge.
//
// Usage: go run ./examples/unique_visitors
package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/dystortion/hyperstats/hll"
)

func main() {
	// Precision 14 gives ~0.81% standard error using ~16 KiB.
	sketch := hll.New(14)

	const realUniqueUsers = 250_000
	const totalEvents = 5_000_000
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	fmt.Printf("Simulating %d events from %d unique users...\n",
		totalEvents, realUniqueUsers)

	start := time.Now()
	for i := 0; i < totalEvents; i++ {
		// Each user visits multiple times; the duplicates are absorbed.
		userID := strconv.Itoa(rng.Intn(realUniqueUsers))
		sketch.AddString(userID)
	}
	elapsed := time.Since(start)

	estimate := sketch.Estimate()
	relErr := float64(estimate-uint64(realUniqueUsers)) / float64(realUniqueUsers) * 100

	fmt.Printf("\nResults:\n")
	fmt.Printf("  True uniques:      %d\n", realUniqueUsers)
	fmt.Printf("  HLL estimate:      %d\n", estimate)
	fmt.Printf("  Relative error:    %+.3f%%\n", relErr)
	fmt.Printf("  Sketch memory:     %d bytes\n", sketch.MemoryBytes())
	fmt.Printf("  Throughput:        %.1f million events/sec\n",
		float64(totalEvents)/elapsed.Seconds()/1e6)

	// Demonstrate serialisation + cross-shard merge.
	data, _ := sketch.MarshalBinary()
	fmt.Printf("\nSerialised size:     %d bytes (transmittable across services)\n", len(data))

	var restored hll.Sketch
	if err := restored.UnmarshalBinary(data); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}
	if restored.Estimate() != estimate {
		fmt.Printf("WARNING: round-trip estimate differs!\n")
	}

	// Two shards merged.
	shardA := hll.New(14)
	shardB := hll.New(14)
	for i := 0; i < 100_000; i++ {
		shardA.AddString("a-" + strconv.Itoa(i))
		shardB.AddString("b-" + strconv.Itoa(i))
	}
	merged := shardA.Clone()
	if err := merged.Merge(shardB); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}
	fmt.Printf("\nMerged two disjoint 100k-shards: estimate = %d (expect ~200,000)\n",
		merged.Estimate())
}
