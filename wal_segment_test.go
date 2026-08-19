package wisp

import (
	"fmt"
	"path/filepath"
	"testing"
)

func segmentTestConfig(dir string) WispConfig {
	return WispConfig{
		WALPath:                filepath.Join(dir, "wal.log"),
		SSTablePath:            filepath.Join(dir, "data.sst"),
		SSTableBlockSize:       128,
		MemTableFlushThreshold: 256,
		WALMaxSegmentSize:      128,
	}
}

func walSegments(t *testing.T, db *Wisp) []uint64 {
	t.Helper()
	segments, err := db.wal.Segments()
	if err != nil {
		t.Fatalf("Segments() error = %v", err)
	}
	return segments
}

// The WAL used to grow without bound because nothing ever truncated it. With
// segmentation, a flush must reclaim every segment whose records reached an
// SSTable, leaving only the active one.
func TestWALSegmentsReclaimedAfterFlush(t *testing.T) {
	dir := t.TempDir()
	config := segmentTestConfig(dir)

	db, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const records = 200
	for i := 0; i < records; i++ {
		if err := db.Insert(uint64(i), int64(i), []byte(fmt.Sprintf("value-%d", i))); err != nil {
			t.Fatalf("Insert(%d) error = %v", i, err)
		}
	}

	// Writing far more than one segment's worth must not accumulate segments.
	if segments := walSegments(t, db); len(segments) > 8 {
		t.Fatalf("Segments() = %v (%d segments), want the log to stay bounded during writes", segments, len(segments))
	}

	if err := db.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	segments := walSegments(t, db)
	if len(segments) != 1 {
		t.Fatalf("Segments() after Flush = %v, want exactly the active segment", segments)
	}
	if segments[0] != db.wal.CurrentSegment() {
		t.Fatalf("Segments() after Flush = %v, want the active segment %d", segments, db.wal.CurrentSegment())
	}

	for i := 0; i < records; i++ {
		value, found, deleted, err := db.Get(uint64(i), int64(i))
		if err != nil {
			t.Fatalf("Get(%d) error = %v", i, err)
		}
		want := fmt.Sprintf("value-%d", i)
		if !found || deleted || string(value) != want {
			t.Fatalf("Get(%d) = %q found=%v deleted=%v, want %q", i, value, found, deleted, want)
		}
	}
}

// Records written after the last flush live only in the active segments, and
// must survive a restart that never got a clean Close.
func TestRecoverReplaysUnflushedSegmentsAfterCrash(t *testing.T) {
	dir := t.TempDir()
	config := segmentTestConfig(dir)

	db, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() error = %v", err)
	}

	if err := db.Insert(1, 10, []byte("flushed")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := db.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	// These land in the WAL and the memtable, but never reach an SSTable.
	if err := db.Insert(2, 20, []byte("unflushed")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := db.Delete(1, 10); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Simulate a crash: drop the file handles without flushing the memtable.
	if err := db.wal.Close(); err != nil {
		t.Fatalf("wal.Close() error = %v", err)
	}
	if err := db.config.SSTableList.Close(); err != nil {
		t.Fatalf("SSTableList.Close() error = %v", err)
	}

	recovered, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() recover error = %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })

	value, found, deleted, err := recovered.Get(2, 20)
	if err != nil {
		t.Fatalf("Get(2, 20) error = %v", err)
	}
	if !found || deleted || string(value) != "unflushed" {
		t.Fatalf("Get(2, 20) = %q found=%v deleted=%v, want %q", value, found, deleted, "unflushed")
	}

	// The tombstone was also WAL-only, and must win over the flushed value.
	if _, found, deleted, err := recovered.Get(1, 10); err != nil {
		t.Fatalf("Get(1, 10) error = %v", err)
	} else if found || !deleted {
		t.Fatalf("Get(1, 10) found=%v deleted=%v, want found=false deleted=true", found, deleted)
	}
}

// Reclaimed segments must not be replayed again on restart.
func TestRecoverDoesNotReplayReclaimedSegments(t *testing.T) {
	dir := t.TempDir()
	config := segmentTestConfig(dir)

	db, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() error = %v", err)
	}

	const records = 50
	for i := 0; i < records; i++ {
		if err := db.Insert(uint64(i), int64(i), []byte(fmt.Sprintf("value-%d", i))); err != nil {
			t.Fatalf("Insert(%d) error = %v", i, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	// Everything was flushed before Close, so recovery should have had nothing
	// to replay into the memtable.
	if size := reopened.mutableMemTable.Size(); size != 0 {
		t.Fatalf("mutable memtable size after reopen = %d, want 0 (segments should have been reclaimed)", size)
	}

	for i := 0; i < records; i++ {
		value, found, deleted, err := reopened.Get(uint64(i), int64(i))
		if err != nil {
			t.Fatalf("Get(%d) after reopen error = %v", i, err)
		}
		want := fmt.Sprintf("value-%d", i)
		if !found || deleted || string(value) != want {
			t.Fatalf("Get(%d) after reopen = %q found=%v deleted=%v, want %q", i, value, found, deleted, want)
		}
	}
}

// Close on an untouched database should not manufacture an empty SSTable or
// leave a trail of empty segments behind.
func TestCloseWithEmptyMemTableDoesNotRotate(t *testing.T) {
	dir := t.TempDir()
	config := segmentTestConfig(dir)

	for i := 0; i < 3; i++ {
		db, err := CreateWispWithConfig(config)
		if err != nil {
			t.Fatalf("CreateWispWithConfig() iteration %d error = %v", i, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close() iteration %d error = %v", i, err)
		}
	}

	db, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if segments := walSegments(t, db); len(segments) != 1 {
		t.Fatalf("Segments() = %v, want a single segment after repeated empty open/close cycles", segments)
	}
}
