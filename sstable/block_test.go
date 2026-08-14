package sstable

import (
	"bytes"
	"testing"
)

func decodeEntries(t *testing.T, data []byte) []Entry {
	t.Helper()
	var entries []Entry
	for pos := 0; pos < len(data); {
		entryLength := int(uint32(data[pos]) | uint32(data[pos+1])<<8 | uint32(data[pos+2])<<16 | uint32(data[pos+3])<<24)
		pos += 4
		seriesID := uint64(0)
		for i := 0; i < 8; i++ {
			seriesID |= uint64(data[pos+i]) << (8 * i)
		}
		timestamp := int64(0)
		for i := 0; i < 8; i++ {
			timestamp |= int64(data[pos+8+i]) << (8 * i)
		}
		deleted := data[pos+16] != 0
		valueLength := int(uint32(data[pos+17]) | uint32(data[pos+18])<<8 | uint32(data[pos+19])<<16 | uint32(data[pos+20])<<24)
		valueStart := pos + 21
		value := append([]byte(nil), data[valueStart:valueStart+valueLength]...)
		entries = append(entries, Entry{SeriesID: seriesID, Timestamp: timestamp, Deleted: deleted, Value: value})
		pos += entryLength
	}
	return entries
}

func entriesEqual(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].SeriesID != b[i].SeriesID || a[i].Timestamp != b[i].Timestamp || a[i].Deleted != b[i].Deleted || !bytes.Equal(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

func TestBlockEncodeRoundTrip(t *testing.T) {
	entries := []Entry{
		{SeriesID: 1, Timestamp: 10, Value: []byte("alpha")},
		{SeriesID: 1, Timestamp: 20, Deleted: true},
		{SeriesID: 2, Timestamp: -5, Value: []byte{}},
	}
	b := &Block{}
	for _, e := range entries {
		b.Add(e)
	}

	var scratch []byte
	data := b.Encode(&scratch)
	if uint32(len(data)) != b.Size {
		t.Fatalf("Encode() len = %d, want %d", len(data), b.Size)
	}

	got := decodeEntries(t, data)
	if !entriesEqual(got, entries) {
		t.Fatalf("decodeEntries() = %+v, want %+v", got, entries)
	}
}

// TestBlockEncodeReusesScratchSafely covers the buffer-reuse path: encoding a
// smaller block after a larger one must not leak stale bytes from the
// larger, previously-encoded content into the new (shorter) result.
func TestBlockEncodeReusesScratchSafely(t *testing.T) {
	big := &Block{}
	big.Add(Entry{SeriesID: 1, Timestamp: 1, Value: bytes.Repeat([]byte("z"), 200)})

	small := &Block{}
	small.Add(Entry{SeriesID: 2, Timestamp: 2, Value: []byte("hi")})

	var scratch []byte
	_ = big.Encode(&scratch)
	data := small.Encode(&scratch)

	if uint32(len(data)) != small.Size {
		t.Fatalf("Encode() len = %d, want %d", len(data), small.Size)
	}
	got := decodeEntries(t, data)
	if !entriesEqual(got, small.Entries) {
		t.Fatalf("decodeEntries() = %+v, want %+v", got, small.Entries)
	}
}
