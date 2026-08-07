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
}

type Wisp struct {
	config            WispConfig
	wal               *wal.WAL
	mutableMemTable   *memtable.MemTable
	immutableMemTable *memtable.MemTable
	sstable           *sstable.SSTable
	sstableReader     *sstable.Reader
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
	if err := wisp.openSSTable(); err != nil {
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
	if w.sstableReader == nil {
		return nil, false, nil
	}
	return w.sstableReader.Get(seriesID, timestamp)
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
	writer, err := sstable.NewWriter(w.config.SSTablePath, w.config.SSTableBlockSize)
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
	if err := w.wal.Reset(); err != nil {
		w.sstableWriter = nil
		return err
	}
	w.sstableWriter = nil
	w.immutableMemTable = nil
	return w.openSSTable()
}

func (w *Wisp) openSSTable() error {
	if _, err := os.Stat(w.config.SSTablePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if w.sstableReader != nil {
				_ = w.sstableReader.Close()
			}
			w.sstable = nil
			w.sstableReader = nil
			return nil
		}
		return err
	}
	if w.sstableReader != nil {
		w.sstableReader.Close()
	}
	reader, err := sstable.OpenReader(w.config.SSTablePath)
	if err != nil {
		return err
	}
	w.sstable = &sstable.SSTable{Path: w.config.SSTablePath}
	w.sstableReader = reader
	return nil
}

func (w *Wisp) Close() error {
	var closeErr error
	if w.sstableReader != nil {
		if err := w.sstableReader.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		w.sstableReader = nil
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
