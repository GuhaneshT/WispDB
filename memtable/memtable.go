package memtable

import (
	"wisp/skiplist"
)

type MemTable struct {
	skiplist *skiplist.Skiplist
}

func createMemTable() *MemTable {
	return &MemTable{
		skiplist: &skiplist.Skiplist{},
	}
}


