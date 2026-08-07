package memtable

import (
	"bytes"
	"testing"
)

func TestMemTablePutGetFreeze(t *testing.T) {
	table, err := CreateMemTableWithThreshold(64)
	if err != nil {
		t.Fatalf("CreateMemTableWithThreshold() error = %v", err)
	}

	if ok := table.Put(1, 100, []byte("alpha")); !ok {
		t.Fatalf("Put() returned false")
	}

	got, found := table.Get(1, 100)
	if !found {
		t.Fatalf("Get() did not find inserted record")
	}
	if !bytes.Equal(got, []byte("alpha")) {
		t.Fatalf("Get() = %q, want %q", got, "alpha")
	}

	if table.Size() == 0 {
		t.Fatalf("Size() = 0, want > 0")
	}

	table.Freeze()
	if ok := table.Put(2, 200, []byte("beta")); ok {
		t.Fatalf("Put() on frozen table returned true")
	}
}

func TestMemTableDeleteAndLookup(t *testing.T) {
	table, err := CreateMemTableWithThreshold(64)
	if err != nil {
		t.Fatalf("CreateMemTableWithThreshold() error = %v", err)
	}

	if ok := table.Put(1, 100, []byte("alpha")); !ok {
		t.Fatalf("Put() returned false")
	}
	if ok := table.Delete(1, 100); !ok {
		t.Fatalf("Delete() returned false")
	}

	if _, found := table.Get(1, 100); found {
		t.Fatalf("Get() found deleted record")
	}

	_, found, deleted := table.Lookup(1, 100)
	if !found || !deleted {
		t.Fatalf("Lookup() = found=%v deleted=%v, want true true", found, deleted)
	}
}
