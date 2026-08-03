package wisp

import (
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
		w.immutableMemTable = w.mutableMemTable
		w.mutableMemTable, _ = memtable.CreateMemTable()
	}

	w.mutableMemTable.Put(key, value)

	return nil
}
