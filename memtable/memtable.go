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
		skiplist: &skiplist.Skiplist{},
		mutable: true,
	}, nil
}

func (m *MemTable) Put(key string, value []byte) (bool) {
	if !m.mutable {
		return false
	}
	m.skiplist.Insert(key, value)
	m.size += uint64(len(key))
	m.size += uint64(len(value))
	return true

}

func (m *MemTable) Get(key string) ([]byte, bool) {
	node, found := m.skiplist.Search(key)
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


