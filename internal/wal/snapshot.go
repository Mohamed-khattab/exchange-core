package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// Snapshot file format:
//   [Magic:4]["SNAP"][Version:1][InstrumentLen:2][Instrument:N]
//   [SeqNo:8][OrderCount:4]
//   [Order1][Order2]...[OrderN]
//   [CRC32:4] (over everything from Magic to last order)

var snapshotMagic = [4]byte{'S', 'N', 'A', 'P'}

const snapshotVersion = 1

// WriteSnapshot writes a snapshot of all resting orders to disk.
// The snapshot is associated with the given WAL sequence number.
func WriteSnapshot(dir string, seqNo uint64, orders []*models.Order) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating snapshot dir: %w", err)
	}

	path := filepath.Join(dir, fmt.Sprintf("snapshot-%012d.snap", seqNo))
	tmpPath := path + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating snapshot file: %w", err)
	}
	defer f.Close()

	crc := crc32.New(crcTable)
	write := func(data []byte) error {
		crc.Write(data)
		_, err := f.Write(data)
		return err
	}

	// Header
	if err := write(snapshotMagic[:]); err != nil {
		return err
	}
	if err := write([]byte{snapshotVersion}); err != nil {
		return err
	}

	// Instrument (extracted from first order, or empty if no orders)
	instrument := ""
	if len(orders) > 0 {
		instrument = orders[0].Instrument
	}
	var instBuf [2]byte
	binary.LittleEndian.PutUint16(instBuf[:], uint16(len(instrument)))
	if err := write(instBuf[:]); err != nil {
		return err
	}
	if err := write([]byte(instrument)); err != nil {
		return err
	}

	// Sequence number
	var seqBuf [8]byte
	binary.LittleEndian.PutUint64(seqBuf[:], seqNo)
	if err := write(seqBuf[:]); err != nil {
		return err
	}

	// Order count
	var countBuf [4]byte
	binary.LittleEndian.PutUint32(countBuf[:], uint32(len(orders)))
	if err := write(countBuf[:]); err != nil {
		return err
	}

	// Orders (reuse the encoding format from WAL events)
	var buf [512]byte
	for _, order := range orders {
		n := encodeOrderForSnapshot(buf[:], order)
		if err := write(buf[:n]); err != nil {
			return err
		}
	}

	// Write CRC
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc.Sum32())
	if _, err := f.Write(crcBuf[:]); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// Atomic rename
	return os.Rename(tmpPath, path)
}

// LoadSnapshot reads the latest snapshot from disk.
// Returns the WAL sequence number at the time of the snapshot and the resting orders.
func LoadSnapshot(dir string) (seqNo uint64, orders []*models.Order, err error) {
	files, err := snapshotFiles(dir)
	if err != nil || len(files) == 0 {
		return 0, nil, err
	}

	// Use the latest snapshot
	path := files[len(files)-1]
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, fmt.Errorf("reading snapshot: %w", err)
	}

	if len(data) < 4+1+2+8+4+4 { // minimum: magic+version+instLen+seqno+count+crc
		return 0, nil, fmt.Errorf("snapshot too short")
	}

	// Verify CRC (over all data except the last 4 bytes)
	body := data[:len(data)-4]
	storedCRC := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.Checksum(body, crcTable) != storedCRC {
		return 0, nil, fmt.Errorf("snapshot CRC mismatch")
	}

	off := 0

	// Magic
	if string(data[off:off+4]) != string(snapshotMagic[:]) {
		return 0, nil, fmt.Errorf("invalid snapshot magic")
	}
	off += 4

	// Version
	if data[off] != snapshotVersion {
		return 0, nil, fmt.Errorf("unsupported snapshot version: %d", data[off])
	}
	off++

	// Instrument
	instLen := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	_ = string(data[off : off+instLen]) // instrument name (available for validation)
	off += instLen

	// Sequence number
	seqNo = binary.LittleEndian.Uint64(data[off:])
	off += 8

	// Order count
	count := int(binary.LittleEndian.Uint32(data[off:]))
	off += 4

	// Decode orders
	orders = make([]*models.Order, 0, count)
	for i := 0; i < count; i++ {
		order, n, err := decodeOrderFromSnapshot(data[off:])
		if err != nil {
			return 0, nil, fmt.Errorf("decoding order %d: %w", i, err)
		}
		orders = append(orders, order)
		off += n
	}

	return seqNo, orders, nil
}

// CleanOldFiles removes WAL files and snapshots older than the given sequence number.
func CleanOldFiles(dir string, keepAfterSeqNo uint64) error {
	// Clean old snapshots (keep only the latest)
	snaps, _ := snapshotFiles(dir)
	if len(snaps) > 1 {
		for _, s := range snaps[:len(snaps)-1] {
			os.Remove(s)
		}
	}

	// Clean WAL files that are fully before the snapshot
	// We keep all WAL files for now -- a more sophisticated approach would
	// parse the first record of each file to determine if it's before the snapshot.
	// For v1, snapshot creation is sufficient to bound replay time.
	return nil
}

func snapshotFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "snapshot-*.snap"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// encodeOrderForSnapshot encodes an order for the snapshot file.
// Returns the number of bytes written.
func encodeOrderForSnapshot(buf []byte, order *models.Order) int {
	off := 0

	binary.LittleEndian.PutUint64(buf[off:], order.ID)
	off += 8
	off += putString(buf[off:], order.ClientID)
	off += putString(buf[off:], order.Instrument)
	buf[off] = byte(order.Side)
	off++
	off += putString(buf[off:], string(order.Type))
	binary.LittleEndian.PutUint64(buf[off:], uint64(order.Price))
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], uint64(order.StopPrice))
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], order.Quantity)
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], order.FilledQty)
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], uint64(order.AvgFillPrice))
	off += 8
	off += putString(buf[off:], order.TimeInForce)
	binary.LittleEndian.PutUint64(buf[off:], uint64(order.CreatedAt.UnixNano()))
	off += 8
	buf[off] = byte(order.Status[0]) // first char as status discriminator
	off++

	return off
}

// decodeOrderFromSnapshot decodes an order from snapshot data.
// Returns the order and the number of bytes consumed.
func decodeOrderFromSnapshot(data []byte) (*models.Order, int, error) {
	if len(data) < 8 {
		return nil, 0, fmt.Errorf("data too short")
	}

	off := 0
	id := binary.LittleEndian.Uint64(data[off:])
	off += 8

	clientID, n := getString(data[off:])
	off += n
	instrument, n := getString(data[off:])
	off += n

	side := models.Side(int8(data[off]))
	off++

	orderType, n := getString(data[off:])
	off += n

	price := int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	stopPrice := int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	quantity := binary.LittleEndian.Uint64(data[off:])
	off += 8
	filledQty := binary.LittleEndian.Uint64(data[off:])
	off += 8
	avgFillPrice := int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	timeInForce, n := getString(data[off:])
	off += n

	createdNano := int64(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	statusByte := data[off]
	off++

	var status models.OrderStatus
	switch statusByte {
	case 'N':
		status = models.StatusNew
	case 'P':
		status = models.StatusPartiallyFilled
	default:
		status = models.StatusNew
	}

	order := &models.Order{
		ID:           id,
		ClientID:     clientID,
		Instrument:   instrument,
		Side:         side,
		Type:         models.OrderType(orderType),
		Status:       status,
		Price:        price,
		StopPrice:    stopPrice,
		Quantity:     quantity,
		FilledQty:    filledQty,
		AvgFillPrice: avgFillPrice,
		TimeInForce:  timeInForce,
		CreatedAt:    time.Unix(0, createdNano).UTC(),
		UpdatedAt:    time.Unix(0, createdNano).UTC(),
	}

	return order, off, nil
}
