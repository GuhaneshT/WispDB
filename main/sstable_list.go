package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"wisp/sstable"
)

type SSTableFile struct {
	Path   string
	Gen    uint64
	Reader *sstable.Reader
}

type SSTableList struct {
	tables []*SSTableFile
}

func (l *SSTableList) ensure() {
	if l.tables == nil {
		l.tables = make([]*SSTableFile, 0)
	}
}

func (l *SSTableList) Add(table *SSTableFile) {
	l.ensure()
	l.tables = append(l.tables, table)
	sort.Slice(l.tables, func(i, j int) bool {
		return l.tables[i].Gen > l.tables[j].Gen
	})
}

func (l *SSTableList) Close() error {
	var closeErr error
	for _, table := range l.tables {
		if table.Reader != nil {
			if err := table.Reader.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
			table.Reader = nil
		}
	}
	return closeErr
}

func (l *SSTableList) Load(basePath string) error {
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
		reader, err := sstable.OpenReader(path)
		if err != nil {
			return fmt.Errorf("open sstable %s: %w", path, err)
		}
		l.Add(&SSTableFile{Path: path, Gen: gen, Reader: reader})
	}
	return nil
}

func (l *SSTableList) NewPath(basePath string, gen uint64) string {
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	return fmt.Sprintf("%s_%06d%s", base, gen, ext)
}

