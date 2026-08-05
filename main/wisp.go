package main

import (
	"fmt"
	"wisp/memtable"
	"wisp/sstable"
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
	sstablewriter *sstable.Writer
	
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


//recover on startup, to be added after sstable is implemented
func (w *Wisp) Recover() error {
	records, err := w.wal.Replay()
	if err != nil {
		return err
	}

	for _, record := range records {
		ok := w.mutableMemTable.Put(
			record.SeriesID,
			record.Timestamp,
			record.Value,
		)

		if !ok {
			return fmt.Errorf(
				"failed to recover record: seriesID=%d timestamp=%d",
				record.SeriesID,
				record.Timestamp,
			)
		}
	}

	return nil
}

func (w *Wisp) Flush() error{

	it := w.immutableMemTable.Iterator()

	for it.Valid() {
		key, value := it.Entry()
		// write to SSTable
		fmt.Printf("Flushing to SSTable: SeriesID=%d Timestamp=%d Value=%s\n", key.SeriesID, key.Timestamp, string(value))
		it.Next()
	}

	
	w.immutableMemTable = nil

	return nil
}
