package memtable

import (
	"wisp/skiplist"
)

type MemTable struct {
	skiplist *skiplist.Skiplist
	size	 uint64
	mutable bool
	flushThreshold uint64
}

type MemTableIterator struct {
	iterator *skiplist.Iterator
}


func CreateMemTable() (*MemTable, error) {
	return &MemTable{
		skiplist:       skiplist.NewSkipList(16),
		mutable:        true,
		flushThreshold: 4 * 1024 * 1024, // 4 MB for now
	}, nil
}

func (m *MemTable) Put(seriesID uint64,timestamp int64, value []byte) (bool) {
	if !m.mutable {
		return false
	}
	m.skiplist.Insert(skiplist.Key{SeriesID: seriesID, Timestamp: timestamp}, value)
	m.size += 8 + 8 + uint64(len(value))
	return true

}

func (m *MemTable) Get(seriesID uint64, timestamp int64) ([]byte, bool) {
	node, found := m.skiplist.Search(skiplist.Key{SeriesID: seriesID, Timestamp: timestamp})
	if !found {
		return nil, false
	}
	return node.Value, true
}

func (m *MemTable) Size() uint64 {
	return m.size
}

func (m *MemTable) IsFull() bool {

//tbd
return m.size >= m.flushThreshold
}

func (m *MemTable) Freeze(){
	m.mutable = false
}


