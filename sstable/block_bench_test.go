package sstable

import "testing"

func benchBlock() *Block {
	b := &Block{}
	for i := 0; i < 32; i++ {
		b.Add(Entry{SeriesID: uint64(i), Timestamp: int64(i * 1000), Value: []byte("the quick brown fox jumps over the lazy dog")})
	}
	return b
}

func BenchmarkBlockEncode(b *testing.B) {
	block := benchBlock()
	var scratch []byte
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = block.Encode(&scratch)
	}
}
