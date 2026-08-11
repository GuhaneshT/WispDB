package compactor

import (
	"bytes"
	"path/filepath"
	"testing"
	"wisp/sstable"
)

func TestCompactorMultiSSTableMerge(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "data.sst")

	list := &sstable.SSTableList{}

	// Table 1 (Gen 1)
	path1 := list.NewPath(basePath, 1)
	w1, err := sstable.NewWriter(path1, 64)
	if err != nil {
		t.Fatalf("w1 error: %v", err)
	}
	_ = w1.Add(sstable.Entry{SeriesID: 1, Timestamp: 10, Value: []byte("v1_old")})
	_ = w1.Add(sstable.Entry{SeriesID: 2, Timestamp: 20, Value: []byte("v2_original")})
	if err := w1.Close(); err != nil {
		t.Fatalf("w1 close: %v", err)
	}
	r1, err := sstable.OpenReader(path1)
	if err != nil {
		t.Fatalf("r1 open: %v", err)
	}
	list.Add(&sstable.SSTableFile{Path: path1, Gen: 1, Reader: r1})

	// Table 2 (Gen 2) - Overwrites (1, 10)
	path2 := list.NewPath(basePath, 2)
	w2, err := sstable.NewWriter(path2, 64)
	if err != nil {
		t.Fatalf("w2 error: %v", err)
	}
	_ = w2.Add(sstable.Entry{SeriesID: 1, Timestamp: 10, Value: []byte("v1_new")})
	_ = w2.Add(sstable.Entry{SeriesID: 3, Timestamp: 30, Value: []byte("v3_added")})
	if err := w2.Close(); err != nil {
		t.Fatalf("w2 close: %v", err)
	}
	r2, err := sstable.OpenReader(path2)
	if err != nil {
		t.Fatalf("r2 open: %v", err)
	}
	list.Add(&sstable.SSTableFile{Path: path2, Gen: 2, Reader: r2})

	// Perform Compaction
	comp := NewCompactor(list)
	if err := comp.CompactAll(basePath, true); err != nil {
		t.Fatalf("CompactAll error = %v", err)
	}

	tables := list.GetTables()
	defer sstable.ReleaseTables(tables)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table after compaction, got %d", len(tables))
	}
	if tables[0].Gen != 3 {
		t.Fatalf("expected generation 3, got %d", tables[0].Gen)
	}

	reader := tables[0].Reader

	// Verify key (1, 10) returns updated value "v1_new"
	val, found,_, err := reader.Get(1, 10)
	if err != nil || !found || !bytes.Equal(val, []byte("v1_new")) {
		t.Fatalf("Get(1, 10) = %q, found=%v, err=%v; want v1_new", val, found, err)
	}

	// Verify key (2, 20) returns "v2_original"
	val, found,_, err = reader.Get(2, 20)
	if err != nil || !found || !bytes.Equal(val, []byte("v2_original")) {
		t.Fatalf("Get(2, 20) = %q, found=%v, err=%v; want v2_original", val, found, err)
	}

	// Verify key (3, 30) returns "v3_added"
	val, found,_, err = reader.Get(3, 30)
	if err != nil || !found || !bytes.Equal(val, []byte("v3_added")) {
		t.Fatalf("Get(3, 30) = %q, found=%v, err=%v; want v3_added", val, found, err)
	}

	_ = list.Close()
}

func TestCompactorTombstonePurgeMajor(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "data.sst")
	list := &sstable.SSTableList{}

	// Gen 1: Add (1, 100) = "data"
	path1 := list.NewPath(basePath, 1)
	w1, err := sstable.NewWriter(path1, 64)
	if err != nil {
		t.Fatalf("w1 error: %v", err)
	}
	_ = w1.Add(sstable.Entry{SeriesID: 1, Timestamp: 100, Value: []byte("data")})
	_ = w1.Close()
	r1, err := sstable.OpenReader(path1)
	if err != nil {
		t.Fatalf("r1 open: %v", err)
	}
	list.Add(&sstable.SSTableFile{Path: path1, Gen: 1, Reader: r1})

	// Gen 2: Delete (1, 100)
	path2 := list.NewPath(basePath, 2)
	w2, err := sstable.NewWriter(path2, 64)
	if err != nil {
		t.Fatalf("w2 error: %v", err)
	}
	_ = w2.Add(sstable.Entry{SeriesID: 1, Timestamp: 100, Deleted: true})
	_ = w2.Close()
	r2, err := sstable.OpenReader(path2)
	if err != nil {
		t.Fatalf("r2 open: %v", err)
	}
	list.Add(&sstable.SSTableFile{Path: path2, Gen: 2, Reader: r2})

	// Major compaction (isMajor = true)
	comp := NewCompactor(list)
	if err := comp.CompactAll(basePath, true); err != nil {
		t.Fatalf("CompactAll error = %v", err)
	}

	tables := list.GetTables()
	defer sstable.ReleaseTables(tables)
	// Since all records in Gen1/Gen2 resulted in a purged tombstone, remaining table list should be empty
	if len(tables) != 0 {
		t.Fatalf("expected 0 tables after purging all tombstones, got %d", len(tables))
	}

	_ = list.Close()
}

func TestCompactorTombstonePreserveMinor(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "data.sst")
	list := &sstable.SSTableList{}

	// Gen 1: Delete (1, 100)
	path1 := list.NewPath(basePath, 1)
	w1, err := sstable.NewWriter(path1, 64)
	if err != nil {
		t.Fatalf("w1 error: %v", err)
	}
	_ = w1.Add(sstable.Entry{SeriesID: 1, Timestamp: 100, Deleted: true})
	_ = w1.Close()
	r1, err := sstable.OpenReader(path1)
	if err != nil {
		t.Fatalf("r1 open: %v", err)
	}
	list.Add(&sstable.SSTableFile{Path: path1, Gen: 1, Reader: r1})

	// Minor compaction (isMajor = false)
	comp := NewCompactor(list)
	if err := comp.CompactAll(basePath, false); err != nil {
		t.Fatalf("CompactAll error = %v", err)
	}

	tables := list.GetTables()
	defer sstable.ReleaseTables(tables)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table after minor compaction, got %d", len(tables))
	}

	val, found,deleted, err := tables[0].Reader.Get(1, 100)
	if err != nil || !deleted || found || val != nil {
		t.Fatalf("expected record to remain tombstoned (not found), got found=%v, val=%v", found, val)
	}

	_ = list.Close()
}
