package sstable

import (
	"math"

	"github.com/twmb/murmur3"
)

func hashfunc(key []byte) (uint64, uint64) {
	return murmur3.Sum128(key)
}

type BloomFilter struct {
	Bits      []byte
	NumBits   uint64
	NumHashes uint32
}

func NewBloomFilter(expectedKeys uint64, falsePositiveRate float64) *BloomFilter {
	if expectedKeys == 0 {
		expectedKeys = 1
	}

	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		falsePositiveRate = 0.01
	}

	// Optimal number of bits:
	// m = -(n * ln(p)) / (ln(2)^2)
	numBits := uint64(
		math.Ceil(
			-float64(expectedKeys) * math.Log(falsePositiveRate) /
				(math.Ln2 * math.Ln2),
		),
	)
	if numBits < 1 {
		numBits = 1
	}
	// Optimal number of hash functions:
	// k = (m/n) * ln(2)
	numHashes := uint32(
		math.Round(
			(float64(numBits) / float64(expectedKeys)) * math.Ln2,
		),
	)

	if numHashes < 1 {
		numHashes = 1
	}

	// Convert number of bits to number of bytes.
	numBytes := (numBits + 7) / 8

	return &BloomFilter{
		Bits:      make([]byte, numBytes),
		NumBits:   numBits,
		NumHashes: numHashes,
	}
}

func (b *BloomFilter) Add(key []byte) {
	h1,h2 := hashfunc(key)
	for i := uint32(0); i < b.NumHashes; i++ {
		position := (h1 + uint64(i)*h2) % b.NumBits

		byteIndex := position / 8
		bitIndex := position % 8

		b.Bits[byteIndex] |= 1 << bitIndex
	}
}

func (b *BloomFilter) MayContain(key []byte) bool {
	if b == nil || b.NumBits == 0 || b.NumHashes == 0 {
		return false
	}
	h1,h2 := hashfunc(key)
	for i := uint32(0); i < b.NumHashes; i++ {
		position := (h1 + uint64(i)*h2) % b.NumBits

		byteIndex := position / 8
		bitIndex := position % 8

		if (b.Bits[byteIndex] & (1 << bitIndex)) == 0 {
			return false
		}
	}
	return true
}