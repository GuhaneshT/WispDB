package main

import (
	"errors"
	"fmt"
	"os"
	"wisp/memtable"
	"wisp/sstable"
	"wisp/wal"
)

type Record struct {
	SeriesID  uint64
	Timestamp int64
	Value     []byte
}

const (
	defaultWALPath            = "wal.log"
	defaultSSTablePath        = "data.sst"
	defaultSSTableBlockSize   = sstable.DefaultBlockSize
	defaultMemTableThreshold   = 4 * 1024 * 1024
)

type WispConfig struct {
	WALPath              string
	SSTablePath          string
	SSTableBlockSize     uint32
	MemTableFlushThreshold uint64
	SSTableList          *SSTableList
}

type Wisp struct {
	config            WispConfig
	wal               *wal.WAL
	mutableMemTable   *memtable.MemTable
	immutableMemTable *memtable.MemTable
	sstableWriter     *sstable.Writer
}

func CreateWisp() (*Wisp, error) {
	return CreateWispWithConfig(DefaultWispConfig())
}

func DefaultWispConfig() WispConfig {
	return WispConfig{
		WALPath:               defaultWALPath,
		SSTablePath:           defaultSSTablePath,
		SSTableBlockSize:      defaultSSTableBlockSize,
		MemTableFlushThreshold: defaultMemTableThreshold,
		SSTableList:          &SSTableList{},
	}
}

func CreateWispWithConfig(config WispConfig) (*Wisp, error) {
	if config.WALPath == "" {
		config.WALPath = defaultWALPath
	}
	if config.SSTablePath == "" {
		config.SSTablePath = defaultSSTablePath
	}
	if config.SSTableBlockSize == 0 {
		config.SSTableBlockSize = defaultSSTableBlockSize
	}
	if config.MemTableFlushThreshold == 0 {
		config.MemTableFlushThreshold = defaultMemTableThreshold
	}
	if config.SSTableList == nil {
		config.SSTableList = &SSTableList{}
	}

	walInstance, err := wal.CreateWAL(config.WALPath)
	if err != nil {
		return nil, err
	}
	mutableMemTable, err := memtable.CreateMemTableWithThreshold(config.MemTableFlushThreshold)
	if err != nil {
		_ = walInstance.Close()
		return nil, err
	}
	wisp := &Wisp{config: config, wal: walInstance, mutableMemTable: mutableMemTable}
	if err := wisp.openSSTables(); err != nil {
		_ = wisp.Close()
		return nil, err
	}
	if err := wisp.Recover(); err != nil {
		_ = wisp.Close()
		return nil, err
	}
	return wisp, nil
}

func (w *Wisp) Insert(seriesID uint64, timestamp int64, value []byte) error {
	if err := w.prepareMutableMemTable(); err != nil {
		return err
	}
	record := wal.WALRecord{Timestamp: timestamp, SeriesID: seriesID, Value: value}
	if err := w.wal.AppendRecord(record); err != nil {
		return err
	}
	if !w.mutableMemTable.Put(seriesID, record.Timestamp, value) {
		return fmt.Errorf("failed to put record in mutable memtable")
	}
	return nil
}

func (w *Wisp) Delete(seriesID uint64, timestamp int64) error {
	if err := w.prepareMutableMemTable(); err != nil {
		return err
	}
	record := wal.WALRecord{Timestamp: timestamp, SeriesID: seriesID, Deleted: true}
	if err := w.wal.AppendRecord(record); err != nil {
		return err
	}
	if !w.mutableMemTable.Delete(seriesID, timestamp) {
		return fmt.Errorf("failed to delete record in mutable memtable")
	}
	return nil
}

func (w *Wisp) prepareMutableMemTable() error {
	if w.mutableMemTable.IsFull() {
		w.mutableMemTable.Freeze()
		w.immutableMemTable = w.mutableMemTable
		if err := w.Flush(); err != nil {
			return err
		}
		mutableMemTable, err := memtable.CreateMemTableWithThreshold(w.config.MemTableFlushThreshold)
		if err != nil {
			return err
		}
		w.mutableMemTable = mutableMemTable
	}
	return nil
}

func (w *Wisp) Get(seriesID uint64, timestamp int64) ([]byte, bool, error) {
	if value, found, deleted := w.mutableMemTable.Lookup(seriesID, timestamp); found {
		if deleted {
			return nil, false, nil
		}
		return value, true, nil
	}
	if w.immutableMemTable != nil {
		if value, found, deleted := w.immutableMemTable.Lookup(seriesID, timestamp); found {
			if deleted {
				return nil, false, nil
			}
			return value, true, nil
		}
	}
	for _, table := range w.config.SSTableList.tables {
		value, found, err := table.Reader.Get(seriesID, timestamp)
		if err != nil {
			return nil, false, err
		}
		if found {
			return value, true, nil
		}
	}
	return nil, false, nil
}

// recover on startup, to be added after sstable is implemented
func (w *Wisp) Recover() error {
	records, err := w.wal.Replay()
	if err != nil {
		return err
	}
	for _, record := range records {
		var ok bool
		if record.Deleted {
			ok = w.mutableMemTable.Delete(record.SeriesID, record.Timestamp)
		} else {
			ok = w.mutableMemTable.Put(record.SeriesID, record.Timestamp, record.Value)
		}
		if !ok {
			return fmt.Errorf("failed to recover record: seriesID=%d timestamp=%d", record.SeriesID, record.Timestamp)
		}
	}
	return nil
}

func (w *Wisp) Flush() error {
	if w.immutableMemTable == nil {
		return nil
	}
	var nextGen uint64 = 1
	if n := len(w.config.SSTableList.tables); n > 0 {
		nextGen = w.config.SSTableList.tables[0].Gen + 1
	}
	path := w.config.SSTableList.NewPath(w.config.SSTablePath, nextGen)
	writer, err := sstable.NewWriter(path, w.config.SSTableBlockSize)
	if err != nil {
		return err
	}
	w.sstableWriter = writer
	it := w.immutableMemTable.Iterator()
	for it.Valid() {
		key, value := it.Entry()
		entry := sstable.Entry{SeriesID: key.SeriesID, Timestamp: key.Timestamp, Deleted: it.Deleted(), Value: value}
		if err := writer.Add(entry); err != nil {
			_ = writer.Close()
			w.sstableWriter = nil
			return err
		}
		it.Next()
	}
	if err := writer.Close(); err != nil {
		w.sstableWriter = nil
		return err
	}
	reader, err := sstable.OpenReader(path)
	if err != nil {
		w.sstableWriter = nil
		return err
	}
	w.config.SSTableList.Add(&SSTableFile{Path: path, Gen: nextGen, Reader: reader})
	if err := w.wal.Reset(); err != nil {
		w.sstableWriter = nil
		return err
	}
	w.sstableWriter = nil
	w.immutableMemTable = nil
	return nil
}

func (w *Wisp) openSSTables() error {
	if err := w.config.SSTableList.Load(w.config.SSTablePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

func (w *Wisp) Close() error {
	var closeErr error
	if w.config.SSTableList != nil {
		if err := w.config.SSTableList.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if w.sstableWriter != nil {
		if err := w.sstableWriter.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		w.sstableWriter = nil
	}
	if w.wal != nil {
		if err := w.wal.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		w.wal = nil
	}
	return closeErr
}
