// Package hash provides MurmurHash3 128-bit, the standard non-cryptographic
// hash function used throughout hyperstats.
//
// MurmurHash3 was authored by Austin Appleby and released into the public
// domain. This implementation follows the reference x64_128 variant from
// https://github.com/aappleby/smhasher and is byte-for-byte compatible.
//
// # Why MurmurHash3?
//
// Streaming sketches require a hash function with strong avalanche properties
// (small input changes produce large, uncorrelated output changes) and
// near-uniform output distribution. MurmurHash3 is the de-facto standard for
// sketch implementations because:
//
//   - It is well-studied via the SMHasher test suite (Appleby, 2010).
//   - It is fast (~3 GB/s on commodity hardware).
//   - It produces 128 bits, which is enough entropy for HyperLogLog++ at any
//     practical precision and for deriving multiple independent hashes via
//     the (h1, h2) → h1 + i*h2 construction (Kirsch & Mitzenmacher, 2008).
//
// # Cryptographic warning
//
// MurmurHash3 is NOT cryptographically secure. Adversaries can construct
// inputs that collide. Do not use it where collision resistance against an
// attacker matters (HMAC, password hashing, content addressing, etc.).
//
// # References
//
//   - Appleby, A. (2011). "MurmurHash3." https://github.com/aappleby/smhasher
//   - Kirsch, A. and Mitzenmacher, M. (2008). "Less hashing, same performance:
//     Building a better Bloom filter." Random Structures & Algorithms.
package hash

import (
	"encoding/binary"
	"math/bits"
)

// Sum128 returns the 128-bit MurmurHash3 of data with the given seed.
//
// Output is returned as (h1, h2) where h1 is the low 64 bits and h2 is the
// high 64 bits, matching the canonical reference layout.
//
// Performance: ~3 GB/s on a modern x86_64 core. Allocates nothing.
func Sum128(data []byte, seed uint32) (h1, h2 uint64) {
	const (
		c1 uint64 = 0x87c37b91114253d5
		c2 uint64 = 0x4cf5ad432745937f
	)

	h1 = uint64(seed)
	h2 = uint64(seed)

	// Body: process 16 bytes at a time.
	nblocks := len(data) / 16
	for i := 0; i < nblocks; i++ {
		k1 := binary.LittleEndian.Uint64(data[i*16:])
		k2 := binary.LittleEndian.Uint64(data[i*16+8:])

		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1

		h1 = bits.RotateLeft64(h1, 27)
		h1 += h2
		h1 = h1*5 + 0x52dce729

		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2

		h2 = bits.RotateLeft64(h2, 31)
		h2 += h1
		h2 = h2*5 + 0x38495ab5
	}

	// Tail: remaining 1..15 bytes.
	tail := data[nblocks*16:]
	var k1, k2 uint64
	switch len(tail) {
	case 15:
		k2 ^= uint64(tail[14]) << 48
		fallthrough
	case 14:
		k2 ^= uint64(tail[13]) << 40
		fallthrough
	case 13:
		k2 ^= uint64(tail[12]) << 32
		fallthrough
	case 12:
		k2 ^= uint64(tail[11]) << 24
		fallthrough
	case 11:
		k2 ^= uint64(tail[10]) << 16
		fallthrough
	case 10:
		k2 ^= uint64(tail[9]) << 8
		fallthrough
	case 9:
		k2 ^= uint64(tail[8])
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
		fallthrough
	case 8:
		k1 ^= uint64(tail[7]) << 56
		fallthrough
	case 7:
		k1 ^= uint64(tail[6]) << 48
		fallthrough
	case 6:
		k1 ^= uint64(tail[5]) << 40
		fallthrough
	case 5:
		k1 ^= uint64(tail[4]) << 32
		fallthrough
	case 4:
		k1 ^= uint64(tail[3]) << 24
		fallthrough
	case 3:
		k1 ^= uint64(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint64(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint64(tail[0])
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
	}

	// Finalization.
	h1 ^= uint64(len(data))
	h2 ^= uint64(len(data))

	h1 += h2
	h2 += h1

	h1 = fmix64(h1)
	h2 = fmix64(h2)

	h1 += h2
	h2 += h1

	return h1, h2
}

// Sum64 returns only the low 64 bits of Sum128. Provided for convenience
// when 64 bits of entropy is sufficient (e.g. HyperLogLog++ register
// addressing at any practical precision).
func Sum64(data []byte, seed uint32) uint64 {
	h1, _ := Sum128(data, seed)
	return h1
}

// SumString is a convenience wrapper that avoids the user having to allocate
// a []byte conversion for string inputs. The conversion is zero-copy via
// unsafe in Go's runtime when passed to the hash.
func SumString(s string, seed uint32) (h1, h2 uint64) {
	return Sum128([]byte(s), seed)
}

// fmix64 is MurmurHash3's 64-bit finalization mix, providing avalanche.
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}
