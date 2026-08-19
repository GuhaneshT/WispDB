// Command basic demonstrates the WispDB embedded engine end-to-end:
// insert, point lookup, delete (tombstone), and a range scan.
package main

import (
	"fmt"
	"log"
	"os"

	"wisp"
)

func main() {
	dir, err := os.MkdirTemp("", "wispdb-example-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	config := wisp.DefaultWispConfig()
	config.WALPath = dir + "/wal.log"
	config.SSTablePath = dir + "/data.sst"

	db, err := wisp.CreateWispWithConfig(config)
	if err != nil {
		log.Fatalf("create wisp: %v", err)
	}
	defer db.Close()

	const seriesID uint64 = 1

	fmt.Println("--- Insert ---")
	for _, ts := range []int64{100, 200, 300, 400} {
		value := []byte(fmt.Sprintf("reading-at-%d", ts))
		if err := db.Insert(seriesID, ts, value); err != nil {
			log.Fatalf("insert %d: %v", ts, err)
		}
		fmt.Printf("inserted series=%d ts=%d value=%q\n", seriesID, ts, value)
	}

	fmt.Println("\n--- Get (point lookup) ---")
	value, found, deleted, err := db.Get(seriesID, 200)
	if err != nil {
		log.Fatalf("get: %v", err)
	}
	fmt.Printf("Get(series=%d, ts=200) -> value=%q found=%v deleted=%v\n", seriesID, value, found, deleted)

	fmt.Println("\n--- Delete (tombstone) ---")
	if err := db.Delete(seriesID, 200); err != nil {
		log.Fatalf("delete: %v", err)
	}
	value, found, deleted, err = db.Get(seriesID, 200)
	if err != nil {
		log.Fatalf("get after delete: %v", err)
	}
	fmt.Printf("Get(series=%d, ts=200) after delete -> value=%q found=%v deleted=%v\n", seriesID, value, found, deleted)

	fmt.Println("\n--- Scan (range query) ---")
	it, err := db.Scan(seriesID, 0, 1000)
	if err != nil {
		log.Fatalf("scan: %v", err)
	}
	defer it.Close()

	for it.Next() {
		key, value, _ := it.Entry()
		fmt.Printf("scan -> series=%d ts=%d value=%q\n", key.SeriesID, key.Timestamp, value)
	}
	// 200 is a tombstone, so the scan skips it — only 100, 300, and 400 print.
}
