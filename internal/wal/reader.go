package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Reader reads and replays WAL files for a single instrument.
type Reader struct {
	dir string
}

// NewReader creates a WAL reader for the given instrument.
func NewReader(baseDir, instrument string) *Reader {
	return &Reader{
		dir: filepath.Join(baseDir, instrument),
	}
}

// ReplayHandler is called for each valid WAL record during replay.
type ReplayHandler func(seqNo uint64, eventType uint8, payload []byte) error

// Replay reads all WAL files in order and calls handler for each valid record.
// If afterSeqNo > 0, only records with seqNo > afterSeqNo are passed to the handler.
// Returns the highest sequence number seen.
func (r *Reader) Replay(afterSeqNo uint64, handler ReplayHandler) (uint64, error) {
	files, err := r.walFiles()
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return afterSeqNo, nil
	}

	var maxSeq uint64 = afterSeqNo

	for _, path := range files {
		seq, err := r.replayFile(path, afterSeqNo, handler)
		if err != nil {
			return maxSeq, fmt.Errorf("replaying %s: %w", path, err)
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}

	return maxSeq, nil
}

func (r *Reader) replayFile(path string, afterSeqNo uint64, handler ReplayHandler) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var maxSeq uint64
	lenBuf := make([]byte, 4)

	for {
		// Read record length
		if _, err := io.ReadFull(f, lenBuf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break // end of file or partial record (expected after crash)
			}
			return maxSeq, err
		}

		totalLen := int(binary.LittleEndian.Uint32(lenBuf))
		if totalLen < headerSize || totalLen > 1<<20 { // sanity: max 1MB per record
			break // corrupt or unexpected data, stop replaying this file
		}

		// Read the full record (we already have the first 4 bytes)
		record := make([]byte, totalLen)
		copy(record[:4], lenBuf)
		if _, err := io.ReadFull(f, record[4:]); err != nil {
			break // partial record at end of file
		}

		seqNo, eventType, payload, err := DecodeRecord(record)
		if err != nil {
			// CRC mismatch or malformed record -- stop replaying this file
			break
		}

		if seqNo > maxSeq {
			maxSeq = seqNo
		}

		if seqNo <= afterSeqNo {
			continue // skip records before the snapshot
		}

		if err := handler(seqNo, eventType, payload); err != nil {
			return maxSeq, fmt.Errorf("handler error at seq %d: %w", seqNo, err)
		}
	}

	return maxSeq, nil
}

// walFiles returns all WAL files in the instrument directory, sorted by name.
func (r *Reader) walFiles() ([]string, error) {
	if _, err := os.Stat(r.dir); os.IsNotExist(err) {
		return nil, nil
	}

	matches, err := filepath.Glob(filepath.Join(r.dir, "wal-*.wal"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// HasData returns true if there are any WAL files for this instrument.
func (r *Reader) HasData() bool {
	files, _ := r.walFiles()
	return len(files) > 0
}
