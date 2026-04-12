package wal

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// SyncMode controls how the WAL syncs to disk.
type SyncMode int

const (
	SyncFsync     SyncMode = iota // file.Sync() after every write
	SyncFdatasync                 // fdatasync (Linux) or Sync (macOS) after every write
	SyncNone                      // no sync -- rely on OS page cache
)

// ParseSyncMode converts a config string to a SyncMode.
func ParseSyncMode(s string) SyncMode {
	switch s {
	case "fsync":
		return SyncFsync
	case "fdatasync":
		return SyncFdatasync
	case "none":
		return SyncNone
	default:
		return SyncFdatasync
	}
}

const (
	defaultBufSize  = 64 * 1024       // 64KB write buffer
	maxFileSize     = 64 * 1024 * 1024 // 64MB before rotation
)

// Writer is an append-only WAL file writer for a single instrument.
// It is NOT safe for concurrent use -- designed to be called from a single goroutine.
type Writer struct {
	dir          string
	instrument   string
	file         *os.File
	buf          *bufio.Writer
	seqNo        uint64
	bytesWritten int64
	fileSeqNo    int // file rotation counter
	syncMode     SyncMode
}

// NewWriter creates a WAL writer for the given instrument.
// It creates the instrument's WAL directory if it does not exist.
func NewWriter(baseDir, instrument string, syncMode SyncMode) (*Writer, error) {
	dir := filepath.Join(baseDir, instrument)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating WAL dir: %w", err)
	}

	w := &Writer{
		dir:        dir,
		instrument: instrument,
		syncMode:   syncMode,
	}

	if err := w.openNewFile(); err != nil {
		return nil, err
	}

	return w, nil
}

// SetSeqNo sets the starting sequence number (used after replay).
func (w *Writer) SetSeqNo(seqNo uint64) {
	atomic.StoreUint64(&w.seqNo, seqNo)
}

// SeqNo returns the current sequence number.
func (w *Writer) SeqNo() uint64 {
	return atomic.LoadUint64(&w.seqNo)
}

// Append writes a raw WAL record to the log.
// The record must already be encoded via EncodeOrderAdd or EncodeOrderCancel.
func (w *Writer) Append(record []byte) error {
	if _, err := w.buf.Write(record); err != nil {
		return fmt.Errorf("WAL write: %w", err)
	}

	if err := w.buf.Flush(); err != nil {
		return fmt.Errorf("WAL flush: %w", err)
	}

	if err := w.sync(); err != nil {
		return fmt.Errorf("WAL sync: %w", err)
	}

	w.bytesWritten += int64(len(record))

	// Rotate if file is too large
	if w.bytesWritten >= maxFileSize {
		if err := w.rotate(); err != nil {
			return fmt.Errorf("WAL rotate: %w", err)
		}
	}

	return nil
}

// AppendOrderAdd encodes and writes an order-add event.
// Returns the sequence number assigned to this event.
func (w *Writer) AppendOrderAdd(order interface{ GetID() uint64 }, encodeFn func(buf []byte, seqNo uint64) int) (uint64, error) {
	seq := atomic.AddUint64(&w.seqNo, 1)
	var buf [512]byte
	n := encodeFn(buf[:], seq)
	if err := w.Append(buf[:n]); err != nil {
		return 0, err
	}
	return seq, nil
}

// NextSeqNo increments and returns the next sequence number.
func (w *Writer) NextSeqNo() uint64 {
	return atomic.AddUint64(&w.seqNo, 1)
}

// Close flushes and closes the WAL file.
func (w *Writer) Close() error {
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			return err
		}
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// Dir returns the WAL directory for this instrument.
func (w *Writer) Dir() string {
	return w.dir
}

func (w *Writer) openNewFile() error {
	w.fileSeqNo++
	name := filepath.Join(w.dir, fmt.Sprintf("wal-%06d.wal", w.fileSeqNo))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening WAL file: %w", err)
	}
	w.file = f
	w.buf = bufio.NewWriterSize(f, defaultBufSize)
	w.bytesWritten = 0
	return nil
}

func (w *Writer) rotate() error {
	if err := w.buf.Flush(); err != nil {
		return err
	}
	if err := w.sync(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	return w.openNewFile()
}

func (w *Writer) sync() error {
	switch w.syncMode {
	case SyncFsync:
		return w.file.Sync()
	case SyncFdatasync:
		return fdatasync(w.file)
	case SyncNone:
		return nil
	}
	return nil
}

// fdatasync calls the platform-specific fdatasync.
// On macOS/other, it falls back to Sync.
func fdatasync(f *os.File) error {
	return fdatasyncPlatform(f)
}
