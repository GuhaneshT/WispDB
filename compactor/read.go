package compactor

import (
	"wisp/sstable"
)

type Compactor struct {
	sstableList *sstable.SSTableList
}

func NewCompactor(sstableList *sstable.SSTableList) *Compactor {
	return &Compactor{
		sstableList: sstableList,
	}
}
