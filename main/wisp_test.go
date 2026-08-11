package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestWispFlushAndRecover(t *testing.T) {
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
	if err := db.Insert(1, 20, []byte("beta")); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	first, found, deleted, err := db.Get(1, 10)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found || deleted || !bytes.Equal(first, []byte("alpha")) {
		t.Fatalf("Get(1, 10) = %q, found=%v, deleted=%v", first, found, deleted)
	}

	second, found, deleted, err := db.Get(1, 20)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found || deleted || !bytes.Equal(second, []byte("beta")) {
		t.Fatalf("Get(1, 20) = %q, found=%v, deleted=%v", second, found, deleted)
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

	first, found, deleted, err = reopened.Get(1, 10)
	if err != nil {
		t.Fatalf("Get() after reopen error = %v", err)
	}
	if !found || deleted || !bytes.Equal(first, []byte("alpha")) {
		t.Fatalf("Get(1, 10) after reopen = %q, found=%v, deleted=%v", first, found, deleted		)
	}

	second, found, deleted, err = reopened.Get(1, 20)
	if err != nil {
		t.Fatalf("Get() after reopen error = %v", err)
	}
	if !found || deleted || !bytes.Equal(second, []byte("beta")) {
		t.Fatalf("Get(1, 20) after reopen = %q, found=%v, deleted=%v", second, found, deleted)
	}
}
