package wal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type WALRecord struct {
	Timestamp int64
	SeriesID  uint64
	Deleted   bool
	Value     []byte
}

const (
	DefaultMaxSegmentSize uint64 = 4 * 1024 * 1024
	recordHeaderSize             = 8
	maxRecordSize         uint64 = 64 << 20
)

// payload design v2: timestamp + deleted flag + value length + key + value
func serializePayload(record WALRecord) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, record.Timestamp)
	if err != nil {
		return nil, err
	}
	var deleted uint8
	if record.Deleted {
		deleted = 1
	}
	if err := binary.Write(buf, binary.LittleEndian, deleted); err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.LittleEndian, uint32(len(record.Value)))
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.LittleEndian, record.SeriesID)
	if err != nil {
		return nil, err
	}
	buf.Write([]byte(record.Value))
	return buf.Bytes(), nil
}

// desialize payload into WALRecord
func deserializePayload(data []byte) (WALRecord, error) {
	buf := bytes.NewReader(data)
	var timestamp int64
	var deleted uint8
	var valueLen uint32
	err := binary.Read(buf, binary.LittleEndian, &timestamp)
	if err != nil {
		return WALRecord{}, err
	}
	err = binary.Read(buf, binary.LittleEndian, &deleted)
	if err != nil {
		return WALRecord{}, err
	}
	err = binary.Read(buf, binary.LittleEndian, &valueLen)
	if err != nil {
		return WALRecord{}, err
	}
	seriesIdBytes := make([]byte, 8)
	_, err = io.ReadFull(buf, seriesIdBytes) // read seriesID
	if err != nil {
		return WALRecord{}, err
	}
	seriesId := binary.LittleEndian.Uint64(seriesIdBytes)
	valueBytes := make([]byte, valueLen)
	_, err = io.ReadFull(buf, valueBytes)
	if err != nil {
		return WALRecord{}, err
	}
	return WALRecord{Timestamp: timestamp, SeriesID: seriesId, Deleted: deleted != 0, Value: valueBytes}, nil
}

// putRecordHeader writes the on-disk record framing (crc32 of the payload
// followed by the payload length) into the first recordHeaderSize bytes of buf.
func putRecordHeader(buf []byte, payload []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(payload))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
}

// WAL is a segmented write-ahead log. Records are appended to the active
// segment; once a segment exceeds maxSegmentSize, or the engine explicitly
// seals it via Rotate, a new segment is started. Segments are named after the
// base path with a generation suffix: wal.log -> wal_000001.log.
type WAL struct {
	mu             sync.Mutex
	dir            string
	prefix         string
	ext            string
	maxSegmentSize uint64
	segmentID      uint64
	size           uint64
	file           *os.File
}

func CreateWAL(path string) (*WAL, error) {
	return CreateWALWithSegmentSize(path, DefaultMaxSegmentSize)
}

func CreateWALWithSegmentSize(path string, maxSegmentSize uint64) (*WAL, error) {
	if maxSegmentSize == 0 {
		maxSegmentSize = DefaultMaxSegmentSize
	}
	ext := filepath.Ext(path)
	w := &WAL{
		dir:            filepath.Dir(path),
		prefix:         strings.TrimSuffix(filepath.Base(path), ext),
		ext:            ext,
		maxSegmentSize: maxSegmentSize,
	}

	ids, err := w.listSegmentIDs()
	if err != nil {
		return nil, err
	}

	// A pre-segmentation WAL is a single file sitting at the base path. Adopt
	// it as segment 1 rather than silently ignoring the records it holds.
	if len(ids) == 0 {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			if err := os.Rename(path, w.segmentPath(1)); err != nil {
				return nil, fmt.Errorf("migrate legacy wal %s: %w", path, err)
			}
			ids = []uint64{1}
		}
	}

	next := uint64(1)
	if len(ids) > 0 {
		next = ids[len(ids)-1]
	}
	if err := w.openSegment(next); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *WAL) segmentPath(id uint64) string {
	return filepath.Join(w.dir, fmt.Sprintf("%s_%06d%s", w.prefix, id, w.ext))
}

// listSegmentIDs returns the ids of every segment on disk, ascending.
func (w *WAL) listSegmentIDs() ([]uint64, error) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []uint64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, w.prefix+"_") || !strings.HasSuffix(name, w.ext) {
			continue
		}
		idPart := strings.TrimSuffix(strings.TrimPrefix(name, w.prefix+"_"), w.ext)
		id, err := strconv.ParseUint(idPart, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (w *WAL) openSegment(id uint64) error {
	file, err := os.OpenFile(w.segmentPath(id), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	w.file = file
	w.segmentID = id
	w.size = uint64(info.Size())
	return nil
}

// Append record to the active WAL segment, rotating first if it would overflow.
func (w *WAL) AppendRecord(record WALRecord) error {
	payload, err := serializePayload(record)
	if err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return fmt.Errorf("wal is closed")
	}

	recordSize := uint64(recordHeaderSize + len(payload))
	// size > 0 keeps a single oversized record from rotating forever; it gets
	// a segment to itself instead.
	if w.size > 0 && w.size+recordSize > w.maxSegmentSize {
		if _, err := w.rotateLocked(); err != nil {
			return err
		}
	}

	buf := make([]byte, recordSize)
	putRecordHeader(buf, payload)
	copy(buf[recordHeaderSize:], payload)

	n, err := w.file.Write(buf)
	w.size += uint64(n)
	if err != nil {
		return err
	}
	return w.file.Sync()
}

// Rotate seals the active segment and starts a new one. It returns the id of
// the segment that was sealed, which is the highest segment whose records are
// now immutable and eligible for reclamation once they reach an SSTable.
func (w *WAL) Rotate() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}

func (w *WAL) rotateLocked() (uint64, error) {
	sealed := w.segmentID
	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return 0, err
		}
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		w.file = nil
	}
	if err := w.openSegment(sealed + 1); err != nil {
		return 0, err
	}
	return sealed, nil
}

// CurrentSegment returns the id of the segment currently being appended to.
func (w *WAL) CurrentSegment() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.segmentID
}

// Segments returns the ids of every segment currently on disk, ascending.
func (w *WAL) Segments() ([]uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.listSegmentIDs()
}

// RemoveSegmentsUpTo deletes every sealed segment with an id <= id. The active
// segment is never removed, so callers cannot accidentally discard the records
// that have not yet been flushed.
func (w *WAL) RemoveSegmentsUpTo(id uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	ids, err := w.listSegmentIDs()
	if err != nil {
		return err
	}
	var firstErr error
	for _, segmentID := range ids {
		if segmentID > id || segmentID == w.segmentID {
			continue
		}
		if err := os.Remove(w.segmentPath(segmentID)); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Replay reads every segment in order after a restart or crash.
func (w *WAL) Replay() ([]WALRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return nil, err
		}
	}

	ids, err := w.listSegmentIDs()
	if err != nil {
		return nil, err
	}

	var records []WALRecord
	for i, id := range ids {
		// Only the final segment can hold a torn tail from a crash mid-append.
		segmentRecords, err := replaySegment(w.segmentPath(id), i == len(ids)-1)
		if err != nil {
			return nil, fmt.Errorf("replay wal segment %d: %w", id, err)
		}
		records = append(records, segmentRecords...)
	}
	return records, nil
}

func replaySegment(path string, tolerateTornTail bool) ([]WALRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var records []WALRecord
	for {
		header := make([]byte, recordHeaderSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				break
			}
			// A short header is a partial append; anything else is a real
			// read failure.
			if tolerateTornTail && errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		checksum := binary.LittleEndian.Uint32(header[0:4])
		size := binary.LittleEndian.Uint32(header[4:8])
		if uint64(size) > maxRecordSize {
			return nil, fmt.Errorf("WAL record size %d exceeds maximum %d", size, maxRecordSize)
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			if tolerateTornTail && (err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF)) {
				break
			}
			return nil, err
		}
		// A checksum mismatch is always corruption: every append is fsynced, so
		// a crash truncates the tail rather than rewriting a complete record.
		if crc32.ChecksumIEEE(payload) != checksum {
			return nil, fmt.Errorf("WAL checksum mismatch")
		}
		record, err := deserializePayload(payload)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Reset discards every segment and restarts the log from segment 1.
func (w *WAL) Reset() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	ids, err := w.listSegmentIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := os.Remove(w.segmentPath(id)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return w.openSegment(1)
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
