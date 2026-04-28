// heavy_hitters identifies the most-frequent items in a stream using a
// Count-Min Sketch combined with a top-k tracker. Demonstrates the
// canonical CMS use case: surfacing skewed traffic patterns (top URLs,
// top error codes, top API consumers).
//
// Usage: go run ./examples/heavy_hitters
package main

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"github.com/Dyst0rti0n/hyperstats/cms"
)

// topK keeps the k items with highest frequency seen so far.
type topK struct {
	k    int
	heap *minHeap
	seen map[string]bool
}

type item struct {
	key   string
	count uint64
}

type minHeap []item

func (h minHeap) Len() int            { return len(h) }
func (h minHeap) Less(i, j int) bool  { return h[i].count < h[j].count }
func (h minHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x interface{}) { *h = append(*h, x.(item)) }
func (h *minHeap) Pop() interface{}   { n := len(*h); x := (*h)[n-1]; *h = (*h)[:n-1]; return x }

func newTopK(k int) *topK {
	return &topK{k: k, heap: &minHeap{}, seen: make(map[string]bool)}
}

// observe is called with the (key, current-CMS-estimate) for each event.
// It maintains the top-k items efficiently.
func (t *topK) observe(key string, count uint64) {
	if t.seen[key] {
		// Already in heap; update its count by rebuilding (simple/clear,
		// fine for examples — production would use an indexed heap).
		for i, it := range *t.heap {
			if it.key == key {
				(*t.heap)[i].count = count
				heap.Fix(t.heap, i)
				return
			}
		}
		return
	}
	if t.heap.Len() < t.k {
		heap.Push(t.heap, item{key, count})
		t.seen[key] = true
		return
	}
	if count > (*t.heap)[0].count {
		evicted := heap.Pop(t.heap).(item)
		delete(t.seen, evicted.key)
		heap.Push(t.heap, item{key, count})
		t.seen[key] = true
	}
}

func (t *topK) snapshot() []item {
	out := make([]item, t.heap.Len())
	copy(out, *t.heap)
	sort.Slice(out, func(i, j int) bool { return out[i].count > out[j].count })
	return out
}

func main() {
	// ε=0.001, δ=0.01 → w=2719, d=5, ~106 KiB. With a million events
	// the additive bound is εN = 1000.
	sketch := cms.NewWithGuarantees(0.001, 0.01)
	tracker := newTopK(10)

	const totalEvents = 1_000_000
	const popular = 20   // number of "popular" keys
	const tail = 100_000 // number of "tail" keys
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	fmt.Printf("Simulating %d events: %d popular keys, %d tail keys...\n",
		totalEvents, popular, tail)
	fmt.Printf("Sketch dimensions: %dx%d (%.1f KiB)\n",
		sketch.Width(), sketch.Depth(), float64(sketch.MemoryBytes())/1024)

	// Track ground truth alongside CMS so we can validate.
	truth := make(map[string]uint64, popular+tail)

	start := time.Now()
	for i := 0; i < totalEvents; i++ {
		var key string
		if rng.Float64() < 0.6 {
			// 60% of traffic to a few popular keys.
			key = "hot-" + strconv.Itoa(rng.Intn(popular))
		} else {
			key = "tail-" + strconv.Itoa(rng.Intn(tail))
		}
		sketch.AddString(key, 1)
		truth[key]++

		// Periodically check this key against the top-k tracker.
		if i%100 == 0 {
			tracker.observe(key, sketch.CountString(key))
		}
	}
	elapsed := time.Since(start)

	fmt.Printf("\nThroughput:        %.1f million events/sec\n",
		float64(totalEvents)/elapsed.Seconds()/1e6)
	fmt.Printf("Total mass (N):    %d\n", sketch.TotalMass())
	bound := uint64(0.001 * float64(sketch.TotalMass()))
	fmt.Printf("εN bound:          %d (max overestimate per key, w.p. ≥ 0.99)\n", bound)

	fmt.Printf("\nTop 10 by CMS estimate:\n")
	fmt.Printf("  %-15s %12s %12s %12s\n", "key", "true count", "CMS est", "abs error")
	for _, it := range tracker.snapshot() {
		trueCount := truth[it.key]
		// Re-query CMS for the final count (the tracker holds whatever
		// estimate was current the last time we observed this key, which
		// can be stale).
		finalEst := sketch.CountString(it.key)
		err := int64(finalEst) - int64(trueCount)
		fmt.Printf("  %-15s %12d %12d %12d\n", it.key, trueCount, finalEst, err)
	}

	// Demonstrate cross-shard merge.
	a := cms.NewWithGuarantees(0.001, 0.01)
	b := cms.NewWithGuarantees(0.001, 0.01)
	for i := 0; i < 50_000; i++ {
		a.AddString("user-"+strconv.Itoa(rng.Intn(1000)), 1)
		b.AddString("user-"+strconv.Itoa(rng.Intn(1000)), 1)
	}
	if err := a.Merge(b); err != nil {
		fmt.Printf("merge error: %v\n", err)
		return
	}
	fmt.Printf("\nMerged two 50k shards: total mass = %d\n", a.TotalMass())
}
