package wisp

import (
	"container/heap"
	"errors"
	"fmt"
	"os"
	"sync"
	"wisp/compactor"
	"wisp/memtable"
	"wisp/skiplist"
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
	defaultWALMaxSegmentSize = wal.DefaultMaxSegmentSize
)

type scanHeapItem struct {
	key       skiplist.Key
	value     []byte
	deleted   bool
	gen       uint64
	sourceIdx int
	iterator  interface{} //memtable or sstavle
}

type scanMinHeap []scanHeapItem

func (h scanMinHeap) Len() int { return len(h) }

func (h scanMinHeap) Less(i, j int) bool {
	if h[i].key.SeriesID != h[j].key.SeriesID {
		return h[i].key.SeriesID < h[j].key.SeriesID
	}
	if h[i].key.Timestamp != h[j].key.Timestamp {
		return h[i].key.Timestamp < h[j].key.Timestamp
	}
	return h[i].sourceIdx < h[j].sourceIdx
}

func (h scanMinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *scanMinHeap) Push(x interface{}) {
	*h = append(*h, x.(scanHeapItem))
}

func (h *scanMinHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[0 : n-1]
	return item
}

type ScanIterator struct {
	heap       *scanMinHeap
	seriesID   uint64
	startTs    int64
	endTs      int64
	tables     []*sstable.SSTableFile
	lastKey    *skiplist.Key
	valid      bool
	currentKey skiplist.Key
	currentVal []byte
}

// snapshotForScan freezes the mutable memtable (so it's safe to scan
// lock-free) and returns the now-immutable snapshot plus a refcounted
// handle on every SSTable. If a flush is already in flight when this is
// called, it waits for that flush to land — the same wait-then-freeze
// loop prepareMutableMemTableLocked already uses on the write path —
// rather than skipping the freeze, which would either overwrite
// w.immutableMemTable (permanently losing whatever hasn't been flushed
// yet) or leave the mutable memtable's contents out of the scan.
func (w *Wisp) snapshotForScan() (*memtable.MemTable, []*sstable.SSTableFile, error) {
	w.mu.Lock()
	for w.mutableMemTable.Size() > 0 && w.immutableMemTable != nil {
		imm := w.immutableMemTable
		w.mu.Unlock()
		if err := w.flushImmutable(imm); err != nil {
			return nil, nil, err
		}
		w.mu.Lock()
	}
	if w.mutableMemTable.Size() > 0 {
		if err := w.freezeMutableLocked(); err != nil {
			w.mu.Unlock()
			return nil, nil, err
		}
	}
	imm := w.immutableMemTable
	tables := w.config.SSTableList.GetTables()
	w.mu.Unlock()

	return imm, tables, nil
}

func (w *Wisp) Scan(seriesID uint64, startTs, endTs int64) (*ScanIterator, error) {
	imm, tables, err := w.snapshotForScan()
	if err != nil {
		return nil, err
	}

	h := &scanMinHeap{}
	heap.Init(h)

	
	if imm != nil {
		it := imm.Iterator()
		if it.Valid() {
			key, value, deleted := it.Entry()
			heap.Push(h, scanHeapItem{
				key:       key,
				value:     value,
				deleted:   deleted,
				gen:       0,
				sourceIdx: 0,
				iterator:  it,
			})
		}
	}

	
	for idx, tbl := range tables {
		if tbl == nil || tbl.Reader == nil {
			continue
		}
		it := tbl.Reader.Iterator()
		if it.Valid() {
			key := it.Key()
			value := it.Entry().Value
			deleted := it.Entry().Deleted
			heap.Push(h, scanHeapItem{
				key:       key,
				value:     value,
				deleted:   deleted,
				gen:       tbl.Gen,
				sourceIdx: 1 + idx,
				iterator:  it,
			})
		}
	}

	return &ScanIterator{
		heap:     h,
		seriesID: seriesID,
		startTs:  startTs,
		endTs:    endTs,
		tables:   tables,
	}, nil
}

func (it *ScanIterator) Valid() bool {
	return it.valid
}

func (it *ScanIterator) Next() bool {
	if it.heap.Len() == 0 {
		it.valid = false
		return false
	}

	for it.heap.Len() > 0 {
		item := heap.Pop(it.heap).(scanHeapItem)
		key := item.key

		
		var nextItem scanHeapItem
		if item.sourceIdx == 0 {
			// Immutable memtable
			memIt := item.iterator.(*memtable.MemTableIterator)
			memIt.Next()
			if memIt.Valid() {
				k, v, d := memIt.Entry()
				nextItem = scanHeapItem{
					key:       k,
					value:     v,
					deleted:   d,
					gen:       0,
					sourceIdx: 0,
					iterator:  memIt,
				}
				heap.Push(it.heap, nextItem)
			}
		} else {
			// SSTable
			sstIt := item.iterator.(*sstable.Iterator)
			if sstIt.Next() {
				k := sstIt.Key()
				e := sstIt.Entry()
				nextItem = scanHeapItem{
					key:       k,
					value:     e.Value,
					deleted:   e.Deleted,
					gen:       item.gen,
					sourceIdx: item.sourceIdx,
					iterator:  sstIt,
				}
				heap.Push(it.heap, nextItem)
			}
		}

		
		if key.SeriesID != it.seriesID {
			continue
		}

		
		if key.Timestamp < it.startTs || key.Timestamp > it.endTs {
			continue
		}

		
		if it.lastKey != nil && it.lastKey.SeriesID == key.SeriesID && it.lastKey.Timestamp == key.Timestamp {
			continue
		}

		
		if item.deleted {
			it.lastKey = &key
			continue
		}

		it.currentKey = key
		it.currentVal = item.value
		it.lastKey = &key
		it.valid = true
		return true
	}

	it.valid = false
	return false
}

func (it *ScanIterator) Entry() (skiplist.Key, []byte, bool) {
	if !it.valid {
		return skiplist.Key{}, nil, false
	}
	return it.currentKey, it.currentVal, false
}

func (it *ScanIterator) Close() error {
	sstable.ReleaseTables(it.tables)
	return nil
}



type WispConfig struct {
	WALPath                string
	SSTablePath            string
	SSTableBlockSize       uint32
	MemTableFlushThreshold uint64
	WALMaxSegmentSize      uint64
	SSTableList            *sstable.SSTableList
}

type Wisp struct {
	mu                sync.RWMutex
	flushMu           sync.Mutex
	config            WispConfig
	wal               *wal.WAL
	mutableMemTable   *memtable.MemTable
	immutableMemTable *memtable.MemTable
	// immutableWALSegment is the highest WAL segment sealed at the moment the
	// immutable memtable was frozen. Once that memtable reaches an SSTable,
	// every segment up to and including this one is reclaimable.
	immutableWALSegment uint64
	sstableWriter       *sstable.Writer
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
		WALMaxSegmentSize:      defaultWALMaxSegmentSize,
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
	if config.WALMaxSegmentSize == 0 {
		config.WALMaxSegmentSize = defaultWALMaxSegmentSize
	}
	if config.SSTableList == nil {
		config.SSTableList = &sstable.SSTableList{}
	}

	walInstance, err := wal.CreateWALWithSegmentSize(config.WALPath, config.WALMaxSegmentSize)
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
			if err := w.freezeMutableLocked(); err != nil {
				return err
			}
		}
	}
	return nil
}

// freezeMutableLocked seals the mutable memtable and the WAL segments that
// carry its records, then installs a fresh mutable memtable. Callers must hold
// w.mu; appends are serialized by the same lock, so no record can land in the
// sealed segment after the swap.
func (w *Wisp) freezeMutableLocked() error {
	w.mutableMemTable.Freeze()
	w.immutableMemTable = w.mutableMemTable

	sealed, err := w.wal.Rotate()
	if err != nil {
		return err
	}
	w.immutableWALSegment = sealed

	mutableMemTable, err := memtable.CreateMemTableWithThreshold(w.config.MemTableFlushThreshold)
	if err != nil {
		return err
	}
	w.mutableMemTable = mutableMemTable
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
		// An empty memtable has nothing to flush; freezing it anyway would
		// write an entry-less SSTable and burn a WAL segment on every Close.
		if w.mutableMemTable != nil && w.mutableMemTable.Size() > 0 && !w.mutableMemTable.IsFull() {
			if err := w.freezeMutableLocked(); err != nil {
				w.mu.Unlock()
				return err
			}
			imm = w.immutableMemTable
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
	sealedSegment := w.immutableWALSegment
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
		key, value, deleted := it.Entry()
		entry := sstable.Entry{SeriesID: key.SeriesID, Timestamp: key.Timestamp, Deleted: deleted, Value: value}
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
	if w.immutableMemTable == imm {
		w.immutableMemTable = nil
	}
	walInstance := w.wal
	w.mu.Unlock()

	// Only now that the records are durable in an SSTable is it safe to drop
	// the segments that carried them. Ordering matters: reclaiming earlier
	// would lose data on a crash between the two steps.
	if walInstance != nil && sealedSegment > 0 {
		if err := walInstance.RemoveSegmentsUpTo(sealedSegment); err != nil {
			return err
		}
	}

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

// func (w *Wisp) snapshotForScan() (*memtable.MemTable, *memtable.MemTable, []*sstable.SSTableFile, error) {
// 	w.mu.Lock()

// 	for w.immutableMemTable != nil {
// 		imm := w.immutableMemTable
// 		w.mu.Unlock()
// 		if err := w.flushImmutable(imm); err != nil {
// 			w.mu.Lock()
// 			return nil, nil, nil, err
// 		}
// 		w.mu.Lock()
// 	}

// 	if w.mutableMemTable.Size() > 0 {
// 		if err := w.freezeMutableLocked(); err != nil {
// 			w.mu.Unlock()
// 			return nil, nil, nil, err
// 		}
// 	}

// 	mutableMemtable := w.mutableMemTable    
// 	immutableMemtable := w.immutableMemTable 
// 	tables := w.config.SSTableList.GetTables()

// 	w.mu.Unlock()
// 	return mutableMemtable, immutableMemtable, tables, nil
// }

