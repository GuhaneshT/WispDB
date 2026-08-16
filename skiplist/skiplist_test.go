package skiplist

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestInsertSearchAndOverwrite(t *testing.T) {
	rand.Seed(1)

	s := NewSkipList(8)
	s.Insert(Key{SeriesID: 2, Timestamp: 20}, []byte("beta"))
	s.Insert(Key{SeriesID: 1, Timestamp: 10}, []byte("alpha"))
	s.Insert(Key{SeriesID: 1, Timestamp: 10}, []byte("alpha-updated"))

	node, found := s.Search(Key{SeriesID: 1, Timestamp: 10})
	if !found {
		t.Fatalf("Search() did not find existing key")
	}
	if !bytes.Equal(node.Value, []byte("alpha-updated")) {
		t.Fatalf("Search() value = %q, want %q", node.Value, "alpha-updated")
	}

	if _, found := s.Search(Key{SeriesID: 9, Timestamp: 99}); found {
		t.Fatalf("Search() found missing key")
	}

	if s.length != 2 {
		t.Fatalf("length = %d, want 2 unique keys", s.length)
	}
}

func TestDeleteMarksNodeAsDeleted(t *testing.T) {
	rand.Seed(3)

	s := NewSkipList(8)
	s.Insert(Key{SeriesID: 1, Timestamp: 10}, []byte("alpha"))
	s.Delete(Key{SeriesID: 1, Timestamp: 10})

	node, found := s.Search(Key{SeriesID: 1, Timestamp: 10})
	if !found {
		t.Fatalf("Search() did not find deleted key")
	}
	if !node.Deleted {
		t.Fatalf("deleted node was not marked deleted")
	}
	if value, found, deleted := s.Lookup(Key{SeriesID: 1, Timestamp: 10}); !found || !deleted || value != nil {
		t.Fatalf("Lookup() = found=%v deleted=%v value=%q, want true true nil", found, deleted, value)
	}
}

func TestIteratorOrdersBySeriesAndTimestamp(t *testing.T) {
	rand.Seed(2)

	s := NewSkipList(8)
	entries := []struct {
		key   Key
		value string
	}{
		{key: Key{SeriesID: 2, Timestamp: 20}, value: "b"},
		{key: Key{SeriesID: 1, Timestamp: 30}, value: "c"},
		{key: Key{SeriesID: 1, Timestamp: 10}, value: "a"},
		{key: Key{SeriesID: 2, Timestamp: 10}, value: "d"},
	}

	for _, entry := range entries {
		s.Insert(entry.key, []byte(entry.value))
	}

	it := s.Iterator()
	var got []Key
	var values [][]byte
	for it.Valid() {
		key, value, _ := it.Entry()
		got = append(got, key)
		values = append(values, append([]byte(nil), value...))
		it.Next()
	}

	wantKeys := []Key{
		{SeriesID: 1, Timestamp: 10},
		{SeriesID: 1, Timestamp: 30},
		{SeriesID: 2, Timestamp: 10},
		{SeriesID: 2, Timestamp: 20},
	}
	wantValues := [][]byte{
		[]byte("a"),
		[]byte("c"),
		[]byte("d"),
		[]byte("b"),
	}

	if len(got) != len(wantKeys) {
		t.Fatalf("iterator len = %d, want %d", len(got), len(wantKeys))
	}
	for i := range wantKeys {
		if got[i] != wantKeys[i] {
			t.Fatalf("iterator key[%d] = %+v, want %+v", i, got[i], wantKeys[i])
		}
		if !bytes.Equal(values[i], wantValues[i]) {
			t.Fatalf("iterator value[%d] = %q, want %q", i, values[i], wantValues[i])
		}
	}
}
