package sstable

type Iterator struct {
	reader     *Reader
	blockIndex int
	entries    []Entry
	entryIndex int
	valid      bool
}

func (r *Reader) Iterator() *Iterator {
	it := &Iterator{
		reader: r,
	}

	it.loadNextBlock()

	return it

}

func (it *Iterator) Valid() bool {
	return it.valid
}

func (it *Iterator) Key() Entry {
	if !it.valid {
		return Entry{}
	}

	return it.entries[it.entryIndex]
}

func (it *Iterator) Entry() Entry {
	if !it.valid {
		return Entry{}
	}

	return it.entries[it.entryIndex]
}

func (it *Iterator) Next() bool {
	if !it.valid {
		return false
	}

	it.entryIndex++

	if it.entryIndex < len(it.entries) {
		return true
	}

	return it.loadNextBlock()
}

func (it *Iterator) loadNextBlock() bool {
	for it.blockIndex < len(it.reader.index) {

		indexEntry := it.reader.index[it.blockIndex]

		entries, err := it.reader.readBlock(indexEntry)
		if err != nil {
			it.valid = false
			return false
		}

		it.blockIndex++

		if len(entries) == 0 {
			continue
		}

		it.entries = entries
		it.entryIndex = 0
		it.valid = true

		return true
	}

	it.valid = false
	return false
}