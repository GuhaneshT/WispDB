package wal

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestWALAppendReplayAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")

	w, err := CreateWAL(path)
	if err != nil {
		t.Fatalf("CreateWAL() error = %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	want := []WALRecord{
		{SeriesID: 1, Timestamp: 10, Value: []byte("alpha")},
		{SeriesID: 2, Timestamp: 20, Deleted: true},
	}

	for _, record := range want {
		if err := w.AppendRecord(record); err != nil {
			t.Fatalf("AppendRecord() error = %v", err)
		}
	}

	got, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("Replay() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].SeriesID != want[i].SeriesID || got[i].Timestamp != want[i].Timestamp || got[i].Deleted != want[i].Deleted || !bytes.Equal(got[i].Value, want[i].Value) {
			t.Fatalf("Replay()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if err := w.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	got, err = w.Replay()
	if err != nil {
		t.Fatalf("Replay() after reset error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Replay() after reset len = %d, want 0", len(got))
	}
}
