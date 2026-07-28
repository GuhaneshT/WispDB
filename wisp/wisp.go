package wisp

import (
	"time"

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

	// future:
	// memtable
	// sstable
}

func CreateWisp() (*Wisp, error) {

	walInstance, err := wal.CreateWAL(
		"wal.log",
	)

	if err != nil {
		return nil, err
	}

	return &Wisp{
		wal: walInstance,
	}, nil
}

func (w *Wisp) Insert(
	key string,
	value string,
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

	// TODO:
	// Insert into MemTable

	return nil
}
