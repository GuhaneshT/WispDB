package main

import (
	"fmt"
	"wisp/memtable"
	"wisp/wal"
)

type Record struct {
	SeriesID  uint64
	Timestamp int64
	Value     []byte
}

type Wisp struct {
	wal *wal.WAL
	mutableMemTable *memtable.MemTable
	immutableMemTable *memtable.MemTable

	// future:
	// sstable
}

func CreateWisp() (*Wisp, error) {
	walInstance, err := wal.CreateWAL("wal.log")
	if err != nil {
		return nil, err
	}

	mutableMemTable, err := memtable.CreateMemTable()
	if err != nil {
		return nil, err
	}

	return &Wisp{
		wal:               walInstance,
		mutableMemTable:   mutableMemTable,
		immutableMemTable: nil,
	}, nil
}

func (w *Wisp) Insert(
	seriesId uint64,
	timestamp int64,
	value []byte,
) error {

	record := wal.WALRecord{
		Timestamp: timestamp,
		SeriesID:  seriesId,
		Value:     value,
	}

	err := w.wal.AppendRecord(record)

	if err != nil {
		return err
	}

	if w.mutableMemTable.IsFull() {

		w.mutableMemTable.Freeze()
		w.immutableMemTable = w.mutableMemTable
		w.mutableMemTable, _ = memtable.CreateMemTable()
	}

	status := w.mutableMemTable.Put(seriesId, record.Timestamp, value)
	if !status{
		return fmt.Errorf("failed to put record in mutable memtable")
	}

	return nil
}
