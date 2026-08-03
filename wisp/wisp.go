package wisp

import (
	"fmt"
	"time"
	"wisp/memtable"
	"wisp/wal"
)

type Record struct {
	ID        int
	Timestamp int64
	Key       string
	Value     string
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
	key string,
	value []byte,
) error {

	record := wal.WALRecord{
		Timestamp: time.Now().UnixNano(),
		Key: key,
		Value: value,
	}

	err := w.wal.AppendRecord(record)

	if err != nil {
		return err
	}

	if w.mutableMemTable.IsFull() {
		//freeze the current mutable memtable and create a new one
		w.mutableMemTable.Freeze()
		w.immutableMemTable = w.mutableMemTable
		w.mutableMemTable, _ = memtable.CreateMemTable()
	}

	status := w.mutableMemTable.Put(key, value)
	if !status{
		return fmt.Errorf("failed to put record in mutable memtable")
	}

	return nil
}
