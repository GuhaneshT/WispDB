package sstable

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type SSTableFile struct {
	Path   string
	Gen    uint64
	Reader *Reader
}

type SSTableList struct {
	mu     sync.RWMutex
	tables []*SSTableFile
}

func (l *SSTableList) ensure() {
	if l.tables == nil {
		l.tables = make([]*SSTableFile, 0)
	}
}

func (l *SSTableList) GetTables() []*SSTableFile {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.tables == nil {
		return nil
	}
	res := make([]*SSTableFile, len(l.tables))
	copy(res, l.tables)
	return res
}

func (l *SSTableList) Add(table *SSTableFile) {
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
		if old.Reader != nil {
			if err := old.Reader.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
			old.Reader = nil
		}
		if err := os.Remove(old.Path); err != nil && !os.IsNotExist(err) && firstErr == nil {
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
		if table.Reader != nil {
			if err := table.Reader.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
			table.Reader = nil
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
		path := filepath.Join(dir, name)
		reader, err := OpenReader(path)
		if err != nil {
			return fmt.Errorf("open sstable %s: %w", path, err)
		}
		l.tables = append(l.tables, &SSTableFile{Path: path, Gen: gen, Reader: reader})
	}
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
