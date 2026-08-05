package sstable

import (
	"bytes"
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
	return uint32(4 + 8 + 8 + 4 + len(entry.Value))
}

func (b *Block) Encode() ([]byte, error) {
	var buf bytes.Buffer
	for _, entry := range b.Entries {
		entryLength := uint32(8 + 8 + 4 + len(entry.Value))
		if err := binary.Write(&buf, binary.LittleEndian, entryLength); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, entry.SeriesID); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, entry.Timestamp); err != nil {
			return nil, err
		}
		valueLength := uint32(len(entry.Value))
		if err := binary.Write(&buf, binary.LittleEndian, valueLength); err != nil {
			return nil, err
		}
		if _, err := buf.Write(entry.Value); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
