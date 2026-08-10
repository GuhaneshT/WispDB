package sstable

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type SSTableFile struct {
	Path     string
	Gen      uint64
	Reader   *Reader
	refCount atomic.Int32
	unlinked atomic.Bool
}

func NewSSTableFile(path string, gen uint64, reader *Reader) *SSTableFile {
	sf := &SSTableFile{
		Path:   path,
		Gen:    gen,
		Reader: reader,
	}
	sf.refCount.Store(1)
	return sf
}

func (sf *SSTableFile) IncrRef() bool {
	for {
		cur := sf.refCount.Load()
		if cur <= 0 || sf.unlinked.Load() {
			return false
		}
		if sf.refCount.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (sf *SSTableFile) DecrRef() error {
	newRef := sf.refCount.Add(-1)
	if newRef < 0 {
		return nil
	}
	if newRef == 0 {
		var firstErr error
		if sf.Reader != nil {
			if err := sf.Reader.Close(); err != nil {
				firstErr = err
			}
			sf.Reader = nil
		}
		if err := os.Remove(sf.Path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}
	return nil
}

type SSTableList struct {
	mu      sync.RWMutex
	tables  []*SSTableFile
	nextGen atomic.Uint64
}

func (l *SSTableList) NextGen() uint64 {
	return l.nextGen.Add(1)
}

func (l *SSTableList) ensure() {
	if l.tables == nil {
		l.tables = make([]*SSTableFile, 0)
	}
}

func ReleaseTables(tables []*SSTableFile) {
	for _, t := range tables {
		if t != nil {
			_ = t.DecrRef()
		}
	}
}

func (l *SSTableList) GetTables() []*SSTableFile {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.tables == nil {
		return nil
	}
	res := make([]*SSTableFile, 0, len(l.tables))
	for _, t := range l.tables {
		if t.IncrRef() {
			res = append(res, t)
		}
	}
	return res
}

func (l *SSTableList) Add(table *SSTableFile) {
	if table.refCount.Load() == 0 {
		table.refCount.Store(1)
	}
	for {
		cur := l.nextGen.Load()
		if table.Gen <= cur {
			break
		}
		if l.nextGen.CompareAndSwap(cur, table.Gen) {
			break
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensure()
	l.tables = append(l.tables, table)
	sort.Slice(l.tables, func(i, j int) bool {
		return l.tables[i].Gen > l.tables[j].Gen
	})
}

func (l *SSTableList) ReplaceTables(oldTables []*SSTableFile, newTable *SSTableFile) error {
	l.mu.Lock()
	l.ensure()

	oldMap := make(map[string]bool)
	for _, old := range oldTables {
		if old != nil {
			oldMap[old.Path] = true
		}
	}

	var kept []*SSTableFile
	for _, t := range l.tables {
		if !oldMap[t.Path] {
			kept = append(kept, t)
		}
	}

	if newTable != nil {
		if newTable.refCount.Load() == 0 {
			newTable.refCount.Store(1)
		}
		kept = append(kept, newTable)
	}

	sort.Slice(kept, func(i, j int) bool {
		return kept[i].Gen > kept[j].Gen
	})

	l.tables = kept
	l.mu.Unlock()

	var firstErr error
	for _, old := range oldTables {
		if old == nil {
			continue
		}
		old.unlinked.Store(true)
		if err := old.DecrRef(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func (l *SSTableList) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var closeErr error
	for _, table := range l.tables {
		table.unlinked.Store(true)
		if err := table.DecrRef(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	l.tables = nil
	return closeErr
}

func (l *SSTableList) Load(basePath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tables = nil
	dir := filepath.Dir(basePath)
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(filepath.Base(basePath), ext)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var maxGen uint64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, base+"_") || !strings.HasSuffix(name, ext) {
			continue
		}
		genPart := strings.TrimSuffix(strings.TrimPrefix(name, base+"_"), ext)
		gen, err := strconv.ParseUint(genPart, 10, 64)
		if err != nil {
			continue
		}
		if gen > maxGen {
			maxGen = gen
		}
		path := filepath.Join(dir, name)
		reader, err := OpenReader(path)
		if err != nil {
			return fmt.Errorf("open sstable %s: %w", path, err)
		}
		l.tables = append(l.tables, NewSSTableFile(path, gen, reader))
	}
	l.nextGen.Store(maxGen)
	sort.Slice(l.tables, func(i, j int) bool {
		return l.tables[i].Gen > l.tables[j].Gen
	})
	return nil
}

func (l *SSTableList) NewPath(basePath string, gen uint64) string {
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	return fmt.Sprintf("%s_%06d%s", base, gen, ext)
}
