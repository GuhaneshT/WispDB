package main

import (
	"path/filepath"
	"testing"
)

func TestWispDeleteSurvivesFlushAndRestart(t *testing.T) {
	dir := t.TempDir()
	config := WispConfig{
		WALPath:               filepath.Join(dir, "wal.log"),
		SSTablePath:           filepath.Join(dir, "data.sst"),
		SSTableBlockSize:      64,
		MemTableFlushThreshold: 1,
	}

	db, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() error = %v", err)
	}

	if err := db.Insert(1, 10, []byte("alpha")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := db.Delete(1, 10); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := db.Insert(2, 20, []byte("beta")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	if _, found,deleted, err := db.Get(1, 10); err != nil {
		t.Fatalf("Get() error = %v", err)
	} else if found || !deleted{
		t.Fatalf("Get() found deleted record")
	}
	if value, found,deleted, err := db.Get(2, 20); err != nil {
		t.Fatalf("Get() error = %v", err)
	} else if !found || !deleted || string(value) != "beta" {
		t.Fatalf("Get() = %q found=%v, deleted=%v, want beta true", value, found, deleted)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := CreateWispWithConfig(config)
	if err != nil {
		t.Fatalf("CreateWispWithConfig() reopen error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, found, deleted, err := reopened.Get(1, 10); err != nil {
		t.Fatalf("Get() after reopen error = %v", err)
	} else if found || !deleted {
		t.Fatalf("Get() after reopen found deleted record")
	}
	if value, found, deleted, err := reopened.Get(2, 20); err != nil {
		t.Fatalf("Get() after reopen error = %v", err)
	} else if !found || !deleted || string(value) != "beta" {
		t.Fatalf("Get() after reopen = %q found=%v, deleted=%v, want beta true", value, found, deleted)
	}
}
