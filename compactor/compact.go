package compactor

import (
	"container/heap"
	"fmt"
	"os"
	"wisp/sstable"
)

type CompactorOptions struct {
	BlockSize  uint32
	IsMajor    bool
	TargetGen  uint64
	OutputPath string
}

type heapItem struct {
	entry      sstable.Entry
	gen        uint64
	sstableIdx int
	iterator   *sstable.Iterator
}

type minHeap []heapItem

func (h minHeap) Len() int { return len(h) }

func (h minHeap) Less(i, j int) bool {
	if h[i].entry.SeriesID != h[j].entry.SeriesID {
		return h[i].entry.SeriesID < h[j].entry.SeriesID
	}
	if h[i].entry.Timestamp != h[j].entry.Timestamp {
		return h[i].entry.Timestamp < h[j].entry.Timestamp
	}
	// Tie-breaker: Higher generation (newer data) comes out first
	return h[i].gen > h[j].gen
}

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x interface{}) {
	*h = append(*h, x.(heapItem))
}

func (h *minHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[0 : n-1]
	return item
}

// CompactSSTables merges the given list of SSTableFiles into a single new SSTable file.
func CompactSSTables(tables []*sstable.SSTableFile, opts CompactorOptions) (*sstable.SSTableFile, error) {
	if len(tables) == 0 {
		return nil, nil
	}

	if opts.BlockSize == 0 {
		opts.BlockSize = sstable.DefaultBlockSize
	}

	h := &minHeap{}
	heap.Init(h)

	for idx, tbl := range tables {
		if tbl == nil || tbl.Reader == nil {
			continue
		}
		it := tbl.Reader.Iterator()
		if it.Valid() {
			heap.Push(h, heapItem{
				entry:      it.Key(),
				gen:        tbl.Gen,
				sstableIdx: idx,
				iterator:   it,
			})
		}
	}

	if h.Len() == 0 {
		return nil, nil
	}

	writer, err := sstable.NewWriter(opts.OutputPath, opts.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("create compact writer: %w", err)
	}

	var writeErr error
	defer func() {
		if writeErr != nil {
			_ = writer.Close()
			_ = os.Remove(opts.OutputPath)
		}
	}()

	type keyStruct struct {
		seriesID  uint64
		timestamp int64
	}
	var lastKey *keyStruct
	
	for h.Len() > 0 {
		item := heap.Pop(h).(heapItem)
		entry := item.entry

		if item.iterator.Next() {
			heap.Push(h, heapItem{
				entry:      item.iterator.Key(),
				gen:        item.gen,
				sstableIdx: item.sstableIdx,
				iterator:   item.iterator,
			})
		}

		if lastKey != nil && entry.SeriesID == lastKey.seriesID && entry.Timestamp == lastKey.timestamp {
			continue
		}

		lastKey = &keyStruct{
			seriesID:  entry.SeriesID,
			timestamp: entry.Timestamp,
		}

		if entry.Deleted && opts.IsMajor {
			continue
		}

		if writeErr = writer.Add(entry); writeErr != nil {
			return nil, fmt.Errorf("write entry during compaction: %w", writeErr)
		}
	}

	if writeErr = writer.Close(); writeErr != nil {
		return nil, fmt.Errorf("close compact writer: %w", writeErr)
	}

	
	reader, err := sstable.OpenReader(opts.OutputPath)
	if err != nil {
		_ = os.Remove(opts.OutputPath)
		// If footer reading failed because table was empty (e.g. all entries were tombstones purged)
		return nil, nil
	}

	return sstable.NewSSTableFile(opts.OutputPath, opts.TargetGen, reader), nil
}

func (c *Compactor) CompactAll(basePath string, isMajor bool) error {
	if c.sstableList == nil {
		return nil
	}
	tables := c.sstableList.GetTables()
	if len(tables) == 0 {
		return nil
	}

	fmt.Println("=== COMPACTION START ===")
	fmt.Printf("isMajor=%v\n", isMajor)
	fmt.Printf("tables=%d\n", len(tables))

	for _, table := range tables {
		fmt.Printf(
			"TABLE: gen=%d path=%s\n",
			table.Gen,
			table.Path,
		)
	}
	defer sstable.ReleaseTables(tables)

	targetGen := c.sstableList.NextGen()
	outputPath := c.sstableList.NewPath(basePath, targetGen)

	newFile, err := CompactSSTables(tables, CompactorOptions{
		BlockSize:  sstable.DefaultBlockSize,
		IsMajor:    isMajor,
		TargetGen:  targetGen,
		OutputPath: outputPath,
	})
	if err != nil {
		return err
	}

	return c.sstableList.ReplaceTables(tables, newFile)
}

func (c *Compactor) CompactRange(tables []*sstable.SSTableFile, basePath string, isMajor bool) error {
	if len(tables) == 0 {
		return nil
	}
	defer sstable.ReleaseTables(tables)

	targetGen := c.sstableList.NextGen()
	outputPath := c.sstableList.NewPath(basePath, targetGen)

	newFile, err := CompactSSTables(tables, CompactorOptions{
		BlockSize:  sstable.DefaultBlockSize,
		IsMajor:    isMajor,
		TargetGen:  targetGen,
		OutputPath: outputPath,
	})
	if err != nil {
		return err
	}

	return c.sstableList.ReplaceTables(tables, newFile)
}
