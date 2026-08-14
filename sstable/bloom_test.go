package sstable

import (
	"fmt"
	"testing"
)

func TestBloomFilterAddAndMayContain(t *testing.T) {
	bloom := NewBloomFilter(100, 0.01)

	keys := [][]byte{
		[]byte("apple"),
		[]byte("banana"),
		[]byte("orange"),
		[]byte("grape"),
		[]byte("mango"),
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

	keys := [][]byte{
		[]byte("apple"),
		[]byte("banana"),
		[]byte("orange"),
	}

	for _, key := range keys {
		if bloom.MayContain(key) {
			t.Errorf("Empty Bloom filter returned true for key %q", key)
		}
	}
}

func TestBloomFilterDuplicateKeys(t *testing.T) {
	bloom := NewBloomFilter(100, 0.01)

	key := []byte("wisp")

	bloom.Add(key)
	bloom.Add(key)
	bloom.Add(key)

	if !bloom.MayContain(key) {
		t.Error("Bloom filter failed for duplicated key")
	}
}

func TestBloomFilterDifferentKeys(t *testing.T) {
	bloom := NewBloomFilter(1000, 0.01)

	keys := []string{
		"apple",
		"banana",
		"orange",
		"grape",
		"mango",
		"watermelon",
		"pineapple",
		"strawberry",
	}

	for _, key := range keys {
		bloom.Add([]byte(key))
	}

	for _, key := range keys {
		if !bloom.MayContain([]byte(key)) {
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
		key := []byte(fmt.Sprintf("key-%d", i))
		bloom.Add(key)
	}

	// Test completely different keys.
	testKeys := uint64(10000)
	falsePositives := 0

	for i := uint64(0); i < testKeys; i++ {
		key := []byte(fmt.Sprintf("missing-key-%d", i))

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