package wal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

type WALRecord struct {
	Timestamp int64
	SeriesID  uint64
	Value     []byte
}

// payload design v1 : timestamp + value length + key + value
func serializePayload(record WALRecord) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, record.Timestamp)
	if err != nil {
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
	var valueLen uint32
	err := binary.Read(buf, binary.LittleEndian, &timestamp)
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
	return WALRecord{Timestamp: timestamp, SeriesID: seriesId, Value: valueBytes}, nil
}

type WAL struct {
	path string
	file *os.File
}

func CreateWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return &WAL{path: path, file: file}, nil
}

// Append record to WAL
func (w *WAL) AppendRecord(record WALRecord) error {
	payload, err := serializePayload(record)
	if err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	checksum := crc32.ChecksumIEEE(payload)
	err = binary.Write(w.file, binary.LittleEndian, checksum)
	if err != nil {
		return err
	}
	payloadSize := uint32(len(payload))
	err = binary.Write(w.file, binary.LittleEndian, payloadSize)
	if err != nil {
		return err
	}
	_, err = w.file.Write(payload)
	if err != nil {
		return err
	}
	return w.file.Sync()
}

// Replay WAL after crash
func (w *WAL) Replay() ([]WALRecord, error) {
	var records []WALRecord
	_, err := w.file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, err
	}
	for {
		var checksum uint32
		err := binary.Read(w.file, binary.LittleEndian, &checksum)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var size uint32
		err = binary.Read(w.file, binary.LittleEndian, &size)
		if err != nil {
			return nil, err
		}
		payload := make([]byte, size)
		_, err = io.ReadFull(w.file, payload)
		if err != nil {
			return nil, err
		}
		calculated := crc32.ChecksumIEEE(payload)
		if checksum != calculated {
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

func (w *WAL) Reset() error {
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return w.file.Sync()
}

func (w *WAL) Close() error {
	return w.file.Close()
}
