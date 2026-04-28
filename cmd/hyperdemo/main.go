// hyperdemo runs a representative workload across all four sketches in
// hyperstats and reports accuracy, memory, and throughput. It also
// emits CSVs to docs/data/ for use by the README's plots.
//
// Usage:
//
//	go run ./cmd/hyperdemo                 # default workload
//	go run ./cmd/hyperdemo -out docs/data  # custom output dir
//
// The defaults are calibrated to run in ~30 seconds on a modern laptop
// while producing statistically meaningful curves.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/dystortion/hyperstats/cms"
	"github.com/dystortion/hyperstats/hll"
	"github.com/dystortion/hyperstats/kll"
	"github.com/dystortion/hyperstats/tdigest"
)

func main() {
	out := flag.String("out", "docs/data", "directory for CSV output")
	scale := flag.Float64("scale", 1.0, "scale factor for workload size (1.0 = default)")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal("mkdir %s: %v", *out, err)
	}

	banner := func(s string) {
		fmt.Printf("\n\033[1;36m═══ %s ═══\033[0m\n", s)
	}

	banner("Environment")
	fmt.Printf("  Go:       %s\n", runtime.Version())
	fmt.Printf("  GOARCH:   %s\n", runtime.GOARCH)
	fmt.Printf("  GOOS:     %s\n", runtime.GOOS)
	fmt.Printf("  CPUs:     %d\n", runtime.NumCPU())

	banner("Demo 1 — HyperLogLog: cardinality vs precision")
	demoHLLAccuracy(*out, *scale)

	banner("Demo 2 — HyperLogLog: error vs cardinality at p=14")
	demoHLLErrorVsN(*out, *scale)

	banner("Demo 3 — Count-Min Sketch: heavy-hitter fidelity")
	demoCMS(*out, *scale)

	banner("Demo 4 — KLL vs t-digest: rank error across quantiles")
	demoQuantileComparison(*out, *scale)

	banner("Demo 5 — Throughput")
	demoThroughput(*out)

	banner("Demo 6 — Mergeability")
	demoMerge()

	fmt.Printf("\n\033[1;32mDONE.\033[0m  CSVs written to %s/\n", *out)
}

// ---------- Demo 1: HLL accuracy vs precision -------------------------------

func demoHLLAccuracy(outDir string, scale float64) {
	const trials = 30
	n := int(100_000 * scale)
	precisions := []uint8{8, 10, 12, 14, 16}

	type row struct {
		Precision     uint8
		Memory        int
		TheoreticalSE float64
		EmpiricalRMSE float64
	}
	var rows []row

	fmt.Printf("  %-3s  %-8s  %-12s  %-12s\n", "p", "memory", "theo σ", "empirical RMSE")
	fmt.Printf("  %s\n", "----------------------------------------------")
	for _, p := range precisions {
		var sumSq float64
		var memBytes int
		for trial := 0; trial < trials; trial++ {
			s := hll.New(p)
			rng := rand.New(rand.NewSource(int64(trial) + int64(p)*1009))
			var buf [8]byte
			for i := 0; i < n; i++ {
				rng.Read(buf[:])
				s.Add(buf[:])
			}
			est := float64(s.Estimate())
			rel := (est - float64(n)) / float64(n)
			sumSq += rel * rel
			memBytes = s.MemoryBytes()
		}
		rmse := math.Sqrt(sumSq / float64(trials))
		theoSE := 1.04 / math.Sqrt(float64(uint32(1)<<p))
		fmt.Printf("  %-3d  %-8s  %-12s  %-12s\n",
			p, humanBytes(memBytes), pct(theoSE), pct(rmse))
		rows = append(rows, row{p, memBytes, theoSE, rmse})
	}

	writeCSV(filepath.Join(outDir, "hll_accuracy.csv"),
		[]string{"precision", "memory_bytes", "theoretical_sigma", "empirical_rmse"},
		func(w *csv.Writer) {
			for _, r := range rows {
				w.Write([]string{
					strconv.Itoa(int(r.Precision)),
					strconv.Itoa(r.Memory),
					strconv.FormatFloat(r.TheoreticalSE, 'f', 6, 64),
					strconv.FormatFloat(r.EmpiricalRMSE, 'f', 6, 64),
				})
			}
		})
}

// ---------- Demo 2: HLL error vs cardinality at fixed precision -------------

func demoHLLErrorVsN(outDir string, scale float64) {
	const p = 14
	const trials = 20
	cardinalities := []int{
		100, 300, 1000, 3000, 10_000, 30_000,
		100_000, 300_000, 1_000_000,
	}

	type row struct {
		N             int
		EmpiricalRMSE float64
		TheoreticalSE float64
	}
	var rows []row

	theoSE := 1.04 / math.Sqrt(float64(uint32(1)<<p))
	fmt.Printf("  Fixed precision p=%d, theoretical σ=%.4f\n", p, theoSE)
	fmt.Printf("  %-12s  %-12s  %-12s\n", "n", "RMSE", "ratio to σ")
	fmt.Printf("  ----------------------------------------\n")
	for _, n := range cardinalities {
		nScaled := int(float64(n) * scale)
		if nScaled < 100 {
			nScaled = 100
		}
		var sumSq float64
		for trial := 0; trial < trials; trial++ {
			s := hll.New(p)
			rng := rand.New(rand.NewSource(int64(trial)*7919 + int64(n)))
			var buf [8]byte
			for i := 0; i < nScaled; i++ {
				rng.Read(buf[:])
				s.Add(buf[:])
			}
			rel := (float64(s.Estimate()) - float64(nScaled)) / float64(nScaled)
			sumSq += rel * rel
		}
		rmse := math.Sqrt(sumSq / float64(trials))
		fmt.Printf("  %-12d  %-12s  %-12.2f\n", nScaled, pct(rmse), rmse/theoSE)
		rows = append(rows, row{nScaled, rmse, theoSE})
	}

	writeCSV(filepath.Join(outDir, "hll_error_vs_n.csv"),
		[]string{"n", "empirical_rmse", "theoretical_sigma"},
		func(w *csv.Writer) {
			for _, r := range rows {
				w.Write([]string{
					strconv.Itoa(r.N),
					strconv.FormatFloat(r.EmpiricalRMSE, 'f', 6, 64),
					strconv.FormatFloat(r.TheoreticalSE, 'f', 6, 64),
				})
			}
		})
}

// ---------- Demo 3: CMS heavy-hitter fidelity -------------------------------

func demoCMS(outDir string, scale float64) {
	const eps = 0.001
	const delta = 0.01
	totalEvents := int(1_000_000 * scale)
	const popular = 20
	const tail = 50_000

	s := cms.NewWithGuarantees(eps, delta)
	rng := rand.New(rand.NewSource(0xc0ffee))
	truth := make(map[string]uint64)

	for i := 0; i < totalEvents; i++ {
		var k string
		if rng.Float64() < 0.5 {
			k = "hot-" + strconv.Itoa(rng.Intn(popular))
		} else {
			k = "tail-" + strconv.Itoa(rng.Intn(tail))
		}
		s.AddString(k, 1)
		truth[k]++
	}

	N := s.TotalMass()
	bound := uint64(eps * float64(N))
	fmt.Printf("  ε=%.3f δ=%.2f → w=%d, d=%d\n", eps, delta, s.Width(), s.Depth())
	fmt.Printf("  N=%d, εN bound=%d (max overestimate w.p. ≥ 0.99)\n", N, bound)
	fmt.Printf("\n  %-12s  %12s  %12s  %10s  %12s\n",
		"key", "true", "estimate", "abs err", "rel err")
	fmt.Printf("  --------------------------------------------------------\n")

	type row struct {
		Key       string
		True      uint64
		Estimate  uint64
		AbsErr    int64
		RelErr    float64
		IsHot     bool
	}
	var rows []row

	// Heavy hitters.
	for i := 0; i < popular; i++ {
		k := "hot-" + strconv.Itoa(i)
		t := truth[k]
		e := s.CountString(k)
		ae := int64(e) - int64(t)
		re := float64(ae) / float64(t)
		rows = append(rows, row{k, t, e, ae, re, true})
		if i < 5 {
			fmt.Printf("  %-12s  %12d  %12d  %10d  %12.5f\n",
				k, t, e, ae, re)
		}
	}
	fmt.Printf("  ... (%d more hot keys)\n", popular-5)

	// Sample some tail keys.
	tailSample := []int{0, 100, 1000, 10000}
	for _, idx := range tailSample {
		k := "tail-" + strconv.Itoa(idx)
		if t, ok := truth[k]; ok {
			e := s.CountString(k)
			ae := int64(e) - int64(t)
			re := float64(ae) / float64(t)
			rows = append(rows, row{k, t, e, ae, re, false})
			fmt.Printf("  %-12s  %12d  %12d  %10d  %12.5f\n",
				k, t, e, ae, re)
		}
	}

	writeCSV(filepath.Join(outDir, "cms_heavy_hitters.csv"),
		[]string{"key", "true_count", "cms_estimate", "abs_error", "rel_error", "is_hot"},
		func(w *csv.Writer) {
			for _, r := range rows {
				w.Write([]string{
					r.Key,
					strconv.FormatUint(r.True, 10),
					strconv.FormatUint(r.Estimate, 10),
					strconv.FormatInt(r.AbsErr, 10),
					strconv.FormatFloat(r.RelErr, 'f', 6, 64),
					strconv.FormatBool(r.IsHot),
				})
			}
		})
}

// ---------- Demo 4: KLL vs t-digest --------------------------------------

func demoQuantileComparison(outDir string, scale float64) {
	n := int(1_000_000 * scale)
	const k = 200
	const delta = 100.0

	rng := rand.New(rand.NewSource(0x7e57))
	kllSk := kll.NewWithSeed(k, 1)
	tdSk := tdigest.New(delta)
	values := make([]float64, n)
	for i := 0; i < n; i++ {
		// Log-normal latency-like distribution.
		x := math.Exp(rng.NormFloat64()*0.6 + 4.0)
		values[i] = x
		kllSk.Add(x)
		tdSk.Add(x)
	}
	sort.Float64s(values)

	probes := []float64{0.5, 0.75, 0.9, 0.95, 0.99, 0.995, 0.999, 0.9999}
	fmt.Printf("  k=%d (KLL), δ=%g (t-digest), n=%d log-normal samples\n", k, delta, n)
	fmt.Printf("  KLL memory:      %d bytes\n", kllSk.MemoryBytes())
	fmt.Printf("  t-digest memory: %d bytes (%d centroids)\n",
		tdSk.MemoryBytes(), tdSk.CentroidCount())
	fmt.Printf("\n  %-9s %-12s %-12s %-9s %-12s %-9s\n",
		"q", "true", "KLL", "KLL Δrank", "t-digest", "TD Δrank")
	fmt.Printf("  --------------------------------------------------------\n")

	type row struct {
		Q              float64
		True           float64
		KLLValue       float64
		KLLRankErr     float64
		TDValue        float64
		TDRankErr      float64
	}
	var rows []row

	for _, q := range probes {
		idx := int(math.Floor(q * float64(n)))
		if idx >= n {
			idx = n - 1
		}
		trueV := values[idx]

		kllV := kllSk.Quantile(q)
		tdV := tdSk.Quantile(q)

		kllRank := kllSk.Rank(trueV)
		tdRank := tdSk.Rank(trueV)
		kllRankErr := math.Abs(kllRank - q)
		tdRankErr := math.Abs(tdRank - q)

		fmt.Printf("  p%-7.4f  %-12.2f  %-12.2f  %-9.5f  %-12.2f  %-9.5f\n",
			q*100, trueV, kllV, kllRankErr, tdV, tdRankErr)

		rows = append(rows, row{q, trueV, kllV, kllRankErr, tdV, tdRankErr})
	}

	writeCSV(filepath.Join(outDir, "quantile_comparison.csv"),
		[]string{"quantile", "true_value", "kll_value", "kll_rank_err", "tdigest_value", "tdigest_rank_err"},
		func(w *csv.Writer) {
			for _, r := range rows {
				w.Write([]string{
					strconv.FormatFloat(r.Q, 'f', 6, 64),
					strconv.FormatFloat(r.True, 'f', 6, 64),
					strconv.FormatFloat(r.KLLValue, 'f', 6, 64),
					strconv.FormatFloat(r.KLLRankErr, 'f', 6, 64),
					strconv.FormatFloat(r.TDValue, 'f', 6, 64),
					strconv.FormatFloat(r.TDRankErr, 'f', 6, 64),
				})
			}
		})
}

// ---------- Demo 5: Throughput ----------------------------------------------

func demoThroughput(outDir string) {
	const n = 1_000_000

	type row struct {
		Sketch string
		Config string
		NsPerOp float64
		MOpsPerSec float64
	}
	var rows []row

	measure := func(name, cfg string, fn func(int)) {
		// Warm-up.
		fn(10_000)
		start := time.Now()
		fn(n)
		dt := time.Since(start)
		nsPerOp := float64(dt.Nanoseconds()) / float64(n)
		mops := float64(n) / dt.Seconds() / 1e6
		fmt.Printf("  %-18s  %-14s  %8.1f ns/op  %6.2f Mops/s\n",
			name, cfg, nsPerOp, mops)
		rows = append(rows, row{name, cfg, nsPerOp, mops})
	}

	hll14 := hll.New(14)
	measure("HyperLogLog", "p=14", func(count int) {
		for i := 0; i < count; i++ {
			hll14.AddString("u-" + strconv.Itoa(i))
		}
	})

	cmsSk := cms.NewWithGuarantees(0.001, 0.01)
	measure("Count-Min", "ε=0.1%,δ=1%", func(count int) {
		for i := 0; i < count; i++ {
			cmsSk.AddString("k-" + strconv.Itoa(i), 1)
		}
	})

	kllSk := kll.NewWithSeed(200, 1)
	measure("KLL", "k=200", func(count int) {
		for i := 0; i < count; i++ {
			kllSk.Add(float64(i))
		}
	})

	tdSk := tdigest.New(100)
	measure("t-digest", "δ=100", func(count int) {
		for i := 0; i < count; i++ {
			tdSk.Add(float64(i))
		}
	})

	writeCSV(filepath.Join(outDir, "throughput.csv"),
		[]string{"sketch", "config", "ns_per_op", "mops_per_sec"},
		func(w *csv.Writer) {
			for _, r := range rows {
				w.Write([]string{
					r.Sketch, r.Config,
					strconv.FormatFloat(r.NsPerOp, 'f', 2, 64),
					strconv.FormatFloat(r.MOpsPerSec, 'f', 2, 64),
				})
			}
		})
}

// ---------- Demo 6: Mergeability --------------------------------------------

func demoMerge() {
	const shards = 8
	const perShard = 25_000
	const truth = shards * perShard // disjoint streams

	a := hll.New(14)
	shardSks := make([]*hll.Sketch, shards)
	for s := 0; s < shards; s++ {
		shardSks[s] = hll.New(14)
		for i := 0; i < perShard; i++ {
			k := fmt.Sprintf("s%d-u%d", s, i)
			shardSks[s].AddString(k)
			a.AddString(k)
		}
	}
	merged := shardSks[0].Clone()
	for s := 1; s < shards; s++ {
		_ = merged.Merge(shardSks[s])
	}

	fmt.Printf("  %d shards × %d uniques each = %d total\n", shards, perShard, truth)
	fmt.Printf("  Sequential HLL:    %d  (rel err %+.3f%%)\n",
		a.Estimate(),
		(float64(a.Estimate())-float64(truth))/float64(truth)*100)
	fmt.Printf("  Sharded + merged:  %d  (rel err %+.3f%%)\n",
		merged.Estimate(),
		(float64(merged.Estimate())-float64(truth))/float64(truth)*100)

	// Round-trip serialise.
	data, _ := merged.MarshalBinary()
	var rt hll.Sketch
	_ = rt.UnmarshalBinary(data)
	fmt.Printf("  Serialised:        %d bytes\n", len(data))
	fmt.Printf("  After round-trip:  %d  (Δ from pre-round-trip: %d)\n",
		rt.Estimate(),
		int64(rt.Estimate())-int64(merged.Estimate()))
}

// ---------- helpers ---------------------------------------------------------

func pct(x float64) string { return fmt.Sprintf("%.4f%%", x*100) }

func humanBytes(b int) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func writeCSV(path string, header []string, fill func(*csv.Writer)) {
	f, err := os.Create(path)
	if err != nil {
		fatal("create %s: %v", path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(header); err != nil {
		fatal("write header: %v", err)
	}
	fill(w)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
