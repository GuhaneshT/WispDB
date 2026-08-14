package sstable

import (
	"bytes"
	"testing"
)

func TestBloomFilterAddAndMayContain(t *testing.T) {
	bloom := NewBloomFilter(100, 0.01)

	keys := []uint64{
		0x6170706c65, // "apple"
		0x62616e616e61, // "banana"
		0x6f72616e6765, // "orange"
		0x6772617065, // "grape"
		0x6d616e676f, // "mango"
	}

	for _, key := range keys {
		bloom.Add(key)
	}

	for _, key := range keys {
		if !bloom.MayContain(key) {
			t.Errorf("Bloom filter returned false for inserted key %q", key)
		}
	}
}

func TestBloomFilterEmpty(t *testing.T) {
	bloom := NewBloomFilter(100, 0.01)

	keys := []uint64{
		0x6170706c65, // "apple"
		0x62616e616e61, // "banana"
		0x6f72616e6765, // "orange"
	}

	for _, key := range keys {
		if bloom.MayContain(key) {
			t.Errorf("Empty Bloom filter returned true for key %q", key)
		}
	}
}

func TestBloomFilterDuplicateKeys(t *testing.T) {
	bloom := NewBloomFilter(100, 0.01)

	key := uint64(0x77697370) // "wisp"

	bloom.Add(key)
	bloom.Add(key)
	bloom.Add(key)

	if !bloom.MayContain(key) {
		t.Error("Bloom filter failed for duplicated key")
	}
}

func TestBloomFilterDifferentKeys(t *testing.T) {
	bloom := NewBloomFilter(1000, 0.01)

	keys := []uint64{
		0x6170706c65, // "apple"
		0x62616e616e61, // "banana"
		0x6f72616e6765, // "orange"
		0x6772617065, // "grape"
		0x6d616e676f, // "mango"
		0x77617465726d, // "watermelon"
		0x70696e656170, // "pineapple"
		0x73747261772d, // "strawberry"
	}

	for _, key := range keys {
		bloom.Add(key)
	}

	for _, key := range keys {
		if !bloom.MayContain(key) {
			t.Errorf("Bloom filter failed for inserted key %q", key)
		}
	}
}

func TestBloomFilterFalsePositiveRate(t *testing.T) {
	expectedKeys := uint64(10000)
	falsePositiveRate := 0.01

	bloom := NewBloomFilter(expectedKeys, falsePositiveRate)

	// Insert keys.
	for i := uint64(0); i < expectedKeys; i++ {
		key := uint64(i)
		bloom.Add(key)
	}

	// Test completely different keys.
	testKeys := uint64(10000)
	falsePositives := 0

	for i := uint64(0); i < testKeys; i++ {
		key := uint64(i + expectedKeys)

		if bloom.MayContain(key) {
			falsePositives++
		}
	}

	actualRate := float64(falsePositives) / float64(testKeys)

	t.Logf(
		"False positives: %d/%d (%.4f%%)",
		falsePositives,
		testKeys,
		actualRate*100,
	)

	// Give some room for statistical variation.
	maxAllowedRate := 0.015

	if actualRate > maxAllowedRate {
		t.Errorf(
			"false positive rate too high: got %.4f, want <= %.4f",
			actualRate,
			maxAllowedRate,
		)
	}
}

func TestBloomFilterEncodeDecodeRoundTrip(t *testing.T) {
	bloom := NewBloomFilter(100, 0.01)
	keys := []uint64{1, 2, 3, 42, 100000}
	for _, key := range keys {
		bloom.Add(key)
	}

	decoded, err := DecodeBloomFilter(bloom.Encode())
	if err != nil {
		t.Fatalf("DecodeBloomFilter() error = %v", err)
	}

	if decoded.NumBits != bloom.NumBits || decoded.NumHashes != bloom.NumHashes {
		t.Errorf("decoded params = (%d, %d), want (%d, %d)", decoded.NumBits, decoded.NumHashes, bloom.NumBits, bloom.NumHashes)
	}
	if !bytes.Equal(decoded.Bits, bloom.Bits) {
		t.Error("decoded bits do not match original")
	}
	for _, key := range keys {
		if !decoded.MayContain(key) {
			t.Errorf("decoded filter returned false for inserted key %d", key)
		}
	}
}

func TestBloomFilterParameters(t *testing.T) {
	expectedKeys := uint64(100000)
	falsePositiveRate := 0.01

	bloom := NewBloomFilter(expectedKeys, falsePositiveRate)

	if bloom.NumBits == 0 {
		t.Error("NumBits should be greater than zero")
	}

	if bloom.NumHashes == 0 {
		t.Error("NumHashes should be greater than zero")
	}

	if len(bloom.Bits) == 0 {
		t.Error("Bloom bitset should not be empty")
	}

	expectedBytes := (bloom.NumBits + 7) / 8

	if uint64(len(bloom.Bits)) != expectedBytes {
		t.Errorf(
			"incorrect bitset size: got %d bytes, want %d",
			len(bloom.Bits),
			expectedBytes,
		)
	}
}