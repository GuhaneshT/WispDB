package sstable

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/twmb/murmur3"
)

func hashfunc(key uint64) (uint64, uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], key)
	return murmur3.Sum128(buf[:])
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

func (b *BloomFilter) Add(key uint64) {
	h1,h2 := hashfunc(key)
	for i := uint32(0); i < b.NumHashes; i++ {
		position := (h1 + uint64(i)*h2) % b.NumBits

		byteIndex := position / 8
		bitIndex := position % 8

		b.Bits[byteIndex] |= 1 << bitIndex
	}
}

// Encode serializes the filter as NumBits(8) | NumHashes(4) | Bits.
func (b *BloomFilter) Encode() []byte {
	buf := make([]byte, 12+len(b.Bits))
	binary.LittleEndian.PutUint64(buf[0:8], b.NumBits)
	binary.LittleEndian.PutUint32(buf[8:12], b.NumHashes)
	copy(buf[12:], b.Bits)
	return buf
}

// DecodeBloomFilter parses the format written by Encode.
func DecodeBloomFilter(data []byte) (*BloomFilter, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("corrupt bloom filter")
	}
	numBits := binary.LittleEndian.Uint64(data[0:8])
	numHashes := binary.LittleEndian.Uint32(data[8:12])
	bits := append([]byte(nil), data[12:]...)
	return &BloomFilter{Bits: bits, NumBits: numBits, NumHashes: numHashes}, nil
}

func (b *BloomFilter) MayContain(key uint64) bool {
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