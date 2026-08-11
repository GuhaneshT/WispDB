package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"wisp/compactor"
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
	defaultWALPath          = "wal.log"
	defaultSSTablePath      = "data.sst"
	defaultSSTableBlockSize = sstable.DefaultBlockSize
	defaultMemTableThreshold = 4 * 1024 * 1024
)

type WispConfig struct {
	WALPath                string
	SSTablePath            string
	SSTableBlockSize       uint32
	MemTableFlushThreshold uint64
	SSTableList            *sstable.SSTableList
}

type Wisp struct {
	mu                sync.RWMutex
	flushMu           sync.Mutex
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
		WALPath:                defaultWALPath,
		SSTablePath:            defaultSSTablePath,
		SSTableBlockSize:       defaultSSTableBlockSize,
		MemTableFlushThreshold: defaultMemTableThreshold,
		SSTableList:            &sstable.SSTableList{},
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
		config.SSTableList = &sstable.SSTableList{}
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
	w.mu.Lock()
	if err := w.prepareMutableMemTableLocked(); err != nil {
		w.mu.Unlock()
		return err
	}
	record := wal.WALRecord{Timestamp: timestamp, SeriesID: seriesID, Value: value}
	if err := w.wal.AppendRecord(record); err != nil {
		w.mu.Unlock()
		return err
	}
	if !w.mutableMemTable.Put(seriesID, record.Timestamp, value) {
		w.mu.Unlock()
		return fmt.Errorf("failed to put record in mutable memtable")
	}
	immToFlush := w.immutableMemTable
	w.mu.Unlock()

	if immToFlush != nil {
		if err := w.flushImmutable(immToFlush); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wisp) Delete(seriesID uint64, timestamp int64) error {
	w.mu.Lock()
	if err := w.prepareMutableMemTableLocked(); err != nil {
		w.mu.Unlock()
		return err
	}
	record := wal.WALRecord{Timestamp: timestamp, SeriesID: seriesID, Deleted: true}
	if err := w.wal.AppendRecord(record); err != nil {
		w.mu.Unlock()
		return err
	}
	if !w.mutableMemTable.Delete(seriesID, timestamp) {
		w.mu.Unlock()
		return fmt.Errorf("failed to delete record in mutable memtable")
	}
	immToFlush := w.immutableMemTable
	w.mu.Unlock()

	if immToFlush != nil {
		if err := w.flushImmutable(immToFlush); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wisp) prepareMutableMemTableLocked() error {
	for w.mutableMemTable.IsFull() {
		if w.immutableMemTable != nil {
			imm := w.immutableMemTable
			w.mu.Unlock()
			if err := w.flushImmutable(imm); err != nil {
				w.mu.Lock()
				return err
			}
			w.mu.Lock()
		} else {
			w.mutableMemTable.Freeze()
			w.immutableMemTable = w.mutableMemTable
			mutableMemTable, err := memtable.CreateMemTableWithThreshold(w.config.MemTableFlushThreshold)
			if err != nil {
				return err
			}
			w.mutableMemTable = mutableMemTable
		}
	}
	return nil
}

func (w *Wisp) Get(seriesID uint64, timestamp int64) ([]byte, bool,bool, error) {
	w.mu.RLock()
	mut := w.mutableMemTable
	imm := w.immutableMemTable
	tables := w.config.SSTableList.GetTables()
	w.mu.RUnlock()

	defer sstable.ReleaseTables(tables)

	if mut != nil {
		if value, found, deleted := mut.Lookup(seriesID, timestamp); found {
			if deleted {
				return nil, false,true, nil
			}
			return value, true, false, nil
		}
	}

	if imm != nil {
		if value, found, deleted := imm.Lookup(seriesID, timestamp); found {
			if deleted {
				return nil, false,true, nil
			}
			return value, true, false, nil
		}
	}

for _, table := range tables {
    value, found, deleted, err := table.Reader.Get(seriesID, timestamp)

    if err != nil {
        return nil, false, deleted, err
    }

    // Tombstone is authoritative.
    // Do NOT search older SSTables.
    if deleted {
        return nil, false, true, nil
    }

    if found {
        return value, true, false, nil
    }
}
	return nil, false, false, nil
}

func (w *Wisp) Compact() error {
	c := compactor.NewCompactor(w.config.SSTableList)
	return c.CompactAll(w.config.SSTablePath, true)
}

func (w *Wisp) Recover() error {
	w.mu.Lock()
	defer w.mu.Unlock()

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
	w.mu.Lock()
	imm := w.immutableMemTable
	if imm == nil {
		if w.mutableMemTable != nil && !w.mutableMemTable.IsFull() {
			w.mutableMemTable.Freeze()
			w.immutableMemTable = w.mutableMemTable
			imm = w.immutableMemTable
			mutableMemTable, err := memtable.CreateMemTableWithThreshold(w.config.MemTableFlushThreshold)
			if err != nil {
				w.mu.Unlock()
				return err
			}
			w.mutableMemTable = mutableMemTable
		}
	}
	w.mu.Unlock()

	if imm != nil {
		return w.flushImmutable(imm)
	}
	return nil
}

func (w *Wisp) flushImmutable(imm *memtable.MemTable) error {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()

	w.mu.RLock()
	currentImm := w.immutableMemTable
	w.mu.RUnlock()
	if currentImm == nil || currentImm != imm {
		return nil
	}

	nextGen := w.config.SSTableList.NextGen()
	path := w.config.SSTableList.NewPath(w.config.SSTablePath, nextGen)
	writer, err := sstable.NewWriter(path, w.config.SSTableBlockSize)
	if err != nil {
		return err
	}

	w.mu.Lock()
	w.sstableWriter = writer
	w.mu.Unlock()

	it := imm.Iterator()
	for it.Valid() {
		key, value := it.Entry()
		entry := sstable.Entry{SeriesID: key.SeriesID, Timestamp: key.Timestamp, Deleted: it.Deleted(), Value: value}
		if err := writer.Add(entry); err != nil {
			_ = writer.Close()
			w.mu.Lock()
			w.sstableWriter = nil
			w.mu.Unlock()
			return err
		}
		it.Next()
	}
	if err := writer.Close(); err != nil {
		w.mu.Lock()
		w.sstableWriter = nil
		w.mu.Unlock()
		return err
	}
	reader, err := sstable.OpenReader(path)
	if err != nil {
		w.mu.Lock()
		w.sstableWriter = nil
		w.mu.Unlock()
		return err
	}

	newSST := sstable.NewSSTableFile(path, nextGen, reader)
	w.config.SSTableList.Add(newSST)

	w.mu.Lock()
	w.sstableWriter = nil
	// if err := w.wal.Reset(); err != nil {
	// 	w.mu.Unlock()
	// 	return err
	// }
	if w.immutableMemTable == imm {
		w.immutableMemTable = nil
	}
	w.mu.Unlock()

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

	if err := w.Flush(); err != nil {
        return err
    }
	w.mu.Lock()
	defer w.mu.Unlock()

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
