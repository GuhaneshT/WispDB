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
	Key       string
	Value     string
}

// payload design v1 : timestamp + key length + value length + key + value
func serializePayload(record WALRecord) ([]byte, error) {

	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, record.Timestamp)
	if err != nil {
		return nil, err
	}

	err = binary.Write(
		buf,
		binary.LittleEndian,
		uint32(len(record.Key)),
	)

	if err != nil {
		return nil, err
	}

	err = binary.Write(
		buf,
		binary.LittleEndian,
		uint32(len(record.Value)),
	)

	if err != nil {
		return nil, err
	}

	buf.Write([]byte(record.Key))
	buf.Write([]byte(record.Value))

	return buf.Bytes(), nil
}

// desialize payload into WALRecord
func deserializePayload(data []byte) (WALRecord, error) {

	buf := bytes.NewReader(data)

	var timestamp int64
	var keyLen uint32
	var valueLen uint32

	err := binary.Read(
		buf,
		binary.LittleEndian,
		&timestamp,
	)

	if err != nil {
		return WALRecord{}, err
	}

	err = binary.Read(
		buf,
		binary.LittleEndian,
		&keyLen,
	)

	if err != nil {
		return WALRecord{}, err
	}

	err = binary.Read(
		buf,
		binary.LittleEndian,
		&valueLen,
	)

	if err != nil {
		return WALRecord{}, err
	}

	keyBytes := make([]byte, keyLen)

	_, err = io.ReadFull(buf, keyBytes)

	if err != nil {
		return WALRecord{}, err
	}

	valueBytes := make([]byte, valueLen)

	_, err = io.ReadFull(buf, valueBytes)

	if err != nil {
		return WALRecord{}, err
	}

	return WALRecord{
		Timestamp: timestamp,
		Key:       string(keyBytes),
		Value:     string(valueBytes),
	}, nil
}

type WAL struct {
	file *os.File
}

// Create WAL file

func CreateWAL(path string) (*WAL, error) {

	file, err := os.OpenFile(
		path,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)

	if err != nil {
		return nil, err
	}

	return &WAL{
		file: file,
	}, nil
}

// Append record to WAL

func (w *WAL) AppendRecord(record WALRecord) error {

	payload, err := serializePayload(record)

	if err != nil {
		return err
	}

	checksum := crc32.ChecksumIEEE(payload)
	err = binary.Write(
		w.file,
		binary.LittleEndian,
		checksum,
	)

	if err != nil {
		return err
	}

	payloadSize := uint32(len(payload))

	err = binary.Write(
		w.file,
		binary.LittleEndian,
		payloadSize,
	)

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

	for {

		var checksum uint32

		err := binary.Read(
			w.file,
			binary.LittleEndian,
			&checksum,
		)

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, err
		}

		var size uint32

		err = binary.Read(
			w.file,
			binary.LittleEndian,
			&size,
		)

		if err != nil {
			return nil, err
		}

		payload := make([]byte, size)

		_, err = io.ReadFull(
			w.file,
			payload,
		)

		if err != nil {
			return nil, err
		}

		calculated := crc32.ChecksumIEEE(payload)

		if checksum != calculated {
			return nil, fmt.Errorf(
				"WAL checksum mismatch",
			)
		}

		record, err := deserializePayload(payload)

		if err != nil {
			return nil, err
		}

		records = append(records, record)

	}

	return records, nil
}
