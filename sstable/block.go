package sstable

import (
	"encoding/binary"
)

type Block struct {
	Entries []Entry
	Size    uint32
}

func (b *Block) Add(entry Entry) {
	b.Entries = append(b.Entries, entry)
	b.Size += entryEncodedSize(entry)
}

func entryEncodedSize(entry Entry) uint32 {
	return uint32(4 + 8 + 8 + 1 + 4 + len(entry.Value))
}

// encodeEntry writes entry into dst using the block-entry layout —
// entryLength + seriesID + timestamp + deleted flag + valueLength + value —
// via plain PutUintXX calls instead of binary.Write, which boxes each scalar
// field into an interface{} (a heap allocation) and allocates its own
// per-call scratch slice on top of that. dst must be exactly
// entryEncodedSize(entry) bytes long.
func encodeEntry(dst []byte, entry Entry) {
	binary.LittleEndian.PutUint32(dst[0:4], uint32(len(dst))-4)
	binary.LittleEndian.PutUint64(dst[4:12], entry.SeriesID)
	binary.LittleEndian.PutUint64(dst[12:20], uint64(entry.Timestamp))
	if entry.Deleted {
		dst[20] = 1
	} else {
		dst[20] = 0
	}
	binary.LittleEndian.PutUint32(dst[21:25], uint32(len(entry.Value)))
	copy(dst[25:], entry.Value)
}

// Encode serializes the block's entries into *scratch, growing or reusing it
// as needed, and returns the result sized to exactly b.Size.
//
// Unlike the WAL's AppendRecord (which encodes concurrently, before a lock,
// and needs a sync.Pool), a Block and its owning Writer are single-owner and
// used strictly sequentially within one flush or compaction — so the caller
// (Writer) can just hold one scratch buffer across every block it writes,
// with no pooling required.
func (b *Block) Encode(scratch *[]byte) []byte {
	buf := *scratch
	if cap(buf) < int(b.Size) {
		buf = make([]byte, b.Size)
	} else {
		buf = buf[:b.Size]
	}
	var offset uint32
	for _, entry := range b.Entries {
		size := entryEncodedSize(entry)
		encodeEntry(buf[offset:offset+size], entry)
		offset += size
	}
	*scratch = buf
	return buf
}
