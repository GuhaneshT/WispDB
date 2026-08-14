package sstable

type Entry struct {
	SeriesID  uint64
	Timestamp int64
	Deleted   bool
	Value     []byte
}

type IndexEntry struct {
	SeriesID  uint64
	Timestamp int64
	Offset    uint64
	Size      uint32
}

type SSTable struct {
	Path       string
	EntryCount uint64
	Iterator  *Iterator
}

const (
	Magic            uint32 = 0x57495350
	Version          uint8  = 1
	DefaultBlockSize uint32 = 4096
	FooterSize               = 52
)

type Footer struct {
	Magic       uint32
	Version     uint8
	IndexOffset uint64
	IndexSize   uint64
	BloomOffset uint64
	BloomSize   uint64
	EntryCount  uint64
	Checksum    uint32

}
