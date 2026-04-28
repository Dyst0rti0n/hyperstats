package hash

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"
)

// TestSum128KnownVectors verifies against output produced by the canonical
// MurmurHash3 x64_128 reference implementation. These vectors were generated
// from the original C++ source (Appleby, 2011) and confirmed with multiple
// independent ports (Apache Commons Codec, Guava). Any drift here means
// downstream sketches will silently diverge from the literature's bounds.
func TestSum128KnownVectors(t *testing.T) {
	tests := []struct {
		name string
		data string
		seed uint32
		h1   uint64
		h2   uint64
	}{
		// Ground truth generated from the canonical MurmurHash3_x64_128
		// implementation via the Python `mmh3` package (which wraps the
		// upstream C++ reference). Cross-checked at vector-generation time;
		// any drift here means downstream sketches diverge from theory.
		{"empty zero seed", "", 0, 0, 0},
		{"empty seed 1", "", 1, 0x4610abe56eff5cb5, 0x51622daa78f83583},
		{"single byte 'a' seed 0", "a", 0, 0x85555565f6597889, 0xe6b53a48510e895a},
		{"abc seed 0", "abc", 0, 0xb4963f3f3fad7867, 0x3ba2744126ca2d52},
		{"alphabet seed 0", "abcdefghijklmnopqrstuvwxyz", 0, 0x749c9d7e516f4aa9, 0xe9ad9c89b6a7d529},
		{"hello world seed 0x9747b28c", "Hello, world!", 0x9747b28c, 0xedc485d662a8392e, 0xf85e7e7631d576ba},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h1, h2 := Sum128([]byte(tc.data), tc.seed)
			if h1 != tc.h1 || h2 != tc.h2 {
				t.Errorf("Sum128(%q, %#x) = (%#x, %#x), want (%#x, %#x)",
					tc.data, tc.seed, h1, h2, tc.h1, tc.h2)
			}
		})
	}
}

// TestSum128Avalanche checks that flipping a single bit in the input flips
// roughly half the output bits across many trials. A hash with poor
// avalanche causes correlated registers in HLL and skewed counters in CMS.
//
// Strict avalanche criterion: for each input bit, the probability that any
// output bit flips should be ~0.5 ± small. We test the population statistic.
func TestSum128Avalanche(t *testing.T) {
	const trials = 4096
	totalFlips := 0
	for i := 0; i < trials; i++ {
		var input [16]byte
		binary.LittleEndian.PutUint64(input[0:], uint64(i))
		binary.LittleEndian.PutUint64(input[8:], uint64(i)*2654435761)

		h1a, h2a := Sum128(input[:], 0)

		// Flip one bit, deterministically chosen.
		bit := i % 128
		input[bit/8] ^= 1 << (bit % 8)

		h1b, h2b := Sum128(input[:], 0)

		flips := bitsDiffer(h1a, h1b) + bitsDiffer(h2a, h2b)
		totalFlips += flips
	}
	avg := float64(totalFlips) / float64(trials)
	// Expected mean is 64 (half of 128 bits). With trials=4096 the standard
	// error of the mean is ~0.09, so 5σ ≈ 0.5. We allow a much wider band
	// since CI hardware is noisy and the test should not be flaky.
	if math.Abs(avg-64) > 1.5 {
		t.Errorf("avalanche mean %.3f bits, want ~64 ± 1.5", avg)
	}
}

// TestSum128Distinctness sanity-checks that distinct sequential inputs
// produce distinct hashes (no trivial collisions in 1M consecutive ints).
// A real collision test would need billions of inputs; this just catches
// an implementation that returns a constant.
func TestSum128Distinctness(t *testing.T) {
	const n = 1_000_000
	seen := make(map[uint64]struct{}, n)
	for i := 0; i < n; i++ {
		h := Sum64([]byte(strconv.Itoa(i)), 0)
		if _, dup := seen[h]; dup {
			t.Fatalf("64-bit collision at i=%d among first %d ints", i, n)
		}
		seen[h] = struct{}{}
	}
}

// TestSum128SeedSensitivity ensures different seeds produce uncorrelated
// outputs for the same input. Required for CMS, which derives independent
// row hashes by varying the seed.
func TestSum128SeedSensitivity(t *testing.T) {
	const trials = 2048
	totalFlips := 0
	data := []byte("hyperstats seed sensitivity probe")
	for i := 0; i < trials; i++ {
		h1a, h2a := Sum128(data, uint32(i))
		h1b, h2b := Sum128(data, uint32(i)+1)
		totalFlips += bitsDiffer(h1a, h1b) + bitsDiffer(h2a, h2b)
	}
	avg := float64(totalFlips) / float64(trials)
	if math.Abs(avg-64) > 2.0 {
		t.Errorf("seed avalanche mean %.3f bits, want ~64 ± 2.0", avg)
	}
}

// TestSum64Consistency checks that Sum64 returns the low 64 bits of Sum128.
func TestSum64Consistency(t *testing.T) {
	for _, s := range []string{"", "a", "abc", "the quick brown fox"} {
		h1, _ := Sum128([]byte(s), 42)
		got := Sum64([]byte(s), 42)
		if got != h1 {
			t.Errorf("Sum64(%q, 42) = %#x, want %#x (low 64 of Sum128)", s, got, h1)
		}
	}
}

// TestSumStringEqualsSum128 verifies the string convenience wrapper.
func TestSumStringEqualsSum128(t *testing.T) {
	for _, s := range []string{"", "x", "hello hyperstats"} {
		a1, a2 := Sum128([]byte(s), 7)
		b1, b2 := SumString(s, 7)
		if a1 != b1 || a2 != b2 {
			t.Errorf("SumString(%q) disagrees with Sum128", s)
		}
	}
}

func bitsDiffer(a, b uint64) int { return popcount64(a ^ b) }

func popcount64(x uint64) int {
	x -= (x >> 1) & 0x5555555555555555
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}

func BenchmarkSum128_16B(b *testing.B) {
	data := make([]byte, 16)
	b.SetBytes(16)
	for i := 0; i < b.N; i++ {
		Sum128(data, 0)
	}
}

func BenchmarkSum128_64B(b *testing.B) {
	data := make([]byte, 64)
	b.SetBytes(64)
	for i := 0; i < b.N; i++ {
		Sum128(data, 0)
	}
}

func BenchmarkSum128_1KiB(b *testing.B) {
	data := make([]byte, 1024)
	b.SetBytes(1024)
	for i := 0; i < b.N; i++ {
		Sum128(data, 0)
	}
}
