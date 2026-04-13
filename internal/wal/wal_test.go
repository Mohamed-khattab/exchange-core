package wal_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/wal"
)

func testOrder() *models.Order {
	return &models.Order{
		ID:          42,
		ClientID:    "test-client-001",
		Instrument:  "BTC-USD",
		Side:        models.SideBuy,
		Type:        models.OrderTypeLimit,
		Status:      models.StatusNew,
		Price:       5000000000000, // 50000.00000000
		StopPrice:   0,
		Quantity:    100000000, // 1.00000000
		TimeInForce: "GTC",
		CreatedAt:   time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
	}
}

func TestEncodeDecodeOrderAdd(t *testing.T) {
	order := testOrder()
	var buf [512]byte
	n := wal.EncodeOrderAdd(buf[:], 1, order)

	seqNo, eventType, payload, err := wal.DecodeRecord(buf[:n])
	if err != nil {
		t.Fatalf("DecodeRecord: %v", err)
	}
	if seqNo != 1 {
		t.Errorf("seqNo = %d, want 1", seqNo)
	}
	if eventType != wal.EventOrderAdd {
		t.Errorf("eventType = %d, want %d", eventType, wal.EventOrderAdd)
	}

	decoded, err := wal.DecodeOrderAdd(payload)
	if err != nil {
		t.Fatalf("DecodeOrderAdd: %v", err)
	}

	if decoded.ID != order.ID {
		t.Errorf("ID = %d, want %d", decoded.ID, order.ID)
	}
	if decoded.ClientID != order.ClientID {
		t.Errorf("ClientID = %s, want %s", decoded.ClientID, order.ClientID)
	}
	if decoded.Instrument != order.Instrument {
		t.Errorf("Instrument = %s, want %s", decoded.Instrument, order.Instrument)
	}
	if decoded.Side != order.Side {
		t.Errorf("Side = %d, want %d", decoded.Side, order.Side)
	}
	if decoded.Type != order.Type {
		t.Errorf("Type = %s, want %s", decoded.Type, order.Type)
	}
	if decoded.Price != order.Price {
		t.Errorf("Price = %d, want %d", decoded.Price, order.Price)
	}
	if decoded.Quantity != order.Quantity {
		t.Errorf("Quantity = %d, want %d", decoded.Quantity, order.Quantity)
	}
	if decoded.TimeInForce != order.TimeInForce {
		t.Errorf("TimeInForce = %s, want %s", decoded.TimeInForce, order.TimeInForce)
	}
	if !decoded.CreatedAt.Equal(order.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", decoded.CreatedAt, order.CreatedAt)
	}
}

func TestEncodeDecodeOrderCancel(t *testing.T) {
	var buf [256]byte
	n := wal.EncodeOrderCancel(buf[:], 5, 42, "BTC-USD")

	seqNo, eventType, payload, err := wal.DecodeRecord(buf[:n])
	if err != nil {
		t.Fatalf("DecodeRecord: %v", err)
	}
	if seqNo != 5 {
		t.Errorf("seqNo = %d, want 5", seqNo)
	}
	if eventType != wal.EventOrderCancel {
		t.Errorf("eventType = %d, want %d", eventType, wal.EventOrderCancel)
	}

	orderID, instrument, tsNano, err := wal.DecodeOrderCancel(payload)
	if err != nil {
		t.Fatalf("DecodeOrderCancel: %v", err)
	}
	if orderID != 42 {
		t.Errorf("orderID = %d, want 42", orderID)
	}
	if instrument != "BTC-USD" {
		t.Errorf("instrument = %s, want BTC-USD", instrument)
	}
	if tsNano == 0 {
		t.Error("expected non-zero event timestamp on encoded cancel")
	}
}

func TestCRCDetectsCorruption(t *testing.T) {
	order := testOrder()
	var buf [512]byte
	n := wal.EncodeOrderAdd(buf[:], 1, order)

	// Corrupt a byte in the payload
	buf[20] ^= 0xFF

	_, _, _, err := wal.DecodeRecord(buf[:n])
	if err == nil {
		t.Error("expected CRC error on corrupted record")
	}
}

func TestWriterAndReaderRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Write some events
	w, err := wal.NewWriter(dir, "BTC-USD", wal.SyncNone)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	order1 := testOrder()
	order1.ID = 1

	order2 := testOrder()
	order2.ID = 2
	order2.Side = models.SideSell

	var buf [512]byte

	seq1 := w.NextSeqNo()
	n := wal.EncodeOrderAdd(buf[:], seq1, order1)
	if err := w.Append(buf[:n]); err != nil {
		t.Fatalf("Append order1: %v", err)
	}

	seq2 := w.NextSeqNo()
	n = wal.EncodeOrderAdd(buf[:], seq2, order2)
	if err := w.Append(buf[:n]); err != nil {
		t.Fatalf("Append order2: %v", err)
	}

	seq3 := w.NextSeqNo()
	n = wal.EncodeOrderCancel(buf[:], seq3, 1, "BTC-USD")
	if err := w.Append(buf[:n]); err != nil {
		t.Fatalf("Append cancel: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read them back
	reader := wal.NewReader(dir, "BTC-USD")
	var events []struct {
		seqNo     uint64
		eventType uint8
	}

	maxSeq, err := reader.Replay(0, func(seqNo uint64, eventType uint8, payload []byte) error {
		events = append(events, struct {
			seqNo     uint64
			eventType uint8
		}{seqNo, eventType})
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].eventType != wal.EventOrderAdd {
		t.Errorf("event 0: type = %d, want OrderAdd", events[0].eventType)
	}
	if events[1].eventType != wal.EventOrderAdd {
		t.Errorf("event 1: type = %d, want OrderAdd", events[1].eventType)
	}
	if events[2].eventType != wal.EventOrderCancel {
		t.Errorf("event 2: type = %d, want OrderCancel", events[2].eventType)
	}
	if maxSeq != seq3 {
		t.Errorf("maxSeq = %d, want %d", maxSeq, seq3)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "BTC-USD")

	orders := []*models.Order{
		{
			ID: 10, ClientID: "c1", Instrument: "BTC-USD",
			Side: models.SideBuy, Type: models.OrderTypeLimit,
			Status: models.StatusNew, Price: 5000000000000, Quantity: 100000000,
			TimeInForce: "GTC", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		{
			ID: 20, ClientID: "c2", Instrument: "BTC-USD",
			Side: models.SideSell, Type: models.OrderTypeLimit,
			Status: models.StatusPartiallyFilled, Price: 5100000000000,
			Quantity: 200000000, FilledQty: 50000000, AvgFillPrice: 5050000000000,
			TimeInForce: "GTC", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
	}

	if err := wal.WriteSnapshot(dir, 500, orders); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	seqNo, loaded, err := wal.LoadSnapshot(dir)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if seqNo != 500 {
		t.Errorf("seqNo = %d, want 500", seqNo)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d orders, want 2", len(loaded))
	}

	if loaded[0].ID != 10 || loaded[0].ClientID != "c1" {
		t.Errorf("order 0: ID=%d ClientID=%s", loaded[0].ID, loaded[0].ClientID)
	}
	if loaded[1].ID != 20 || loaded[1].FilledQty != 50000000 {
		t.Errorf("order 1: ID=%d FilledQty=%d", loaded[1].ID, loaded[1].FilledQty)
	}
	if loaded[1].Status != models.StatusPartiallyFilled {
		t.Errorf("order 1: Status=%s, want PARTIALLY_FILLED", loaded[1].Status)
	}
}

func TestSnapshotCRCDetectsCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ETH-USD")

	orders := []*models.Order{
		{
			ID: 1, ClientID: "c1", Instrument: "ETH-USD",
			Side: models.SideBuy, Type: models.OrderTypeLimit,
			Status: models.StatusNew, Price: 300000000000, Quantity: 100000000,
			TimeInForce: "GTC", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
	}

	if err := wal.WriteSnapshot(dir, 100, orders); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	// Corrupt the file
	files, _ := filepath.Glob(filepath.Join(dir, "snapshot-*.snap"))
	if len(files) == 0 {
		t.Fatal("no snapshot files found")
	}
	data, _ := os.ReadFile(files[0])
	data[len(data)/2] ^= 0xFF
	os.WriteFile(files[0], data, 0o644)

	_, _, err := wal.LoadSnapshot(dir)
	if err == nil {
		t.Error("expected CRC error on corrupted snapshot")
	}
}

func TestParseSyncMode(t *testing.T) {
	cases := map[string]wal.SyncMode{
		"fsync":     wal.SyncFsync,
		"fdatasync": wal.SyncFdatasync,
		"none":      wal.SyncNone,
		"unknown":   wal.SyncFdatasync, // default
	}
	for input, want := range cases {
		if got := wal.ParseSyncMode(input); got != want {
			t.Errorf("ParseSyncMode(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestWriterSetSeqNoAndDir(t *testing.T) {
	dir := t.TempDir()
	w, err := wal.NewWriter(dir, "TEST-USD", wal.SyncNone)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	w.SetSeqNo(42)
	if w.SeqNo() != 42 {
		t.Errorf("SeqNo() = %d, want 42", w.SeqNo())
	}
	if w.Dir() == "" {
		t.Error("Dir() should not be empty")
	}
}

func TestReaderHasDataEmpty(t *testing.T) {
	dir := t.TempDir()
	reader := wal.NewReader(dir, "EMPTY-USD")
	if reader.HasData() {
		t.Error("HasData() should be false for empty dir")
	}
}

func TestReaderHasDataWithFiles(t *testing.T) {
	dir := t.TempDir()
	w, _ := wal.NewWriter(dir, "HAS-USD", wal.SyncNone)
	order := testOrder()
	var buf [512]byte
	seq := w.NextSeqNo()
	n := wal.EncodeOrderAdd(buf[:], seq, order)
	w.Append(buf[:n])
	w.Close()

	reader := wal.NewReader(dir, "HAS-USD")
	if !reader.HasData() {
		t.Error("HasData() should be true")
	}
}

func TestReplayEmptyDir(t *testing.T) {
	dir := t.TempDir()
	reader := wal.NewReader(dir, "NOPE-USD")
	maxSeq, err := reader.Replay(0, func(seqNo uint64, eventType uint8, payload []byte) error {
		t.Error("should not be called on empty dir")
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if maxSeq != 0 {
		t.Errorf("maxSeq = %d, want 0", maxSeq)
	}
}

func TestCleanOldFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "CLEAN-USD")
	orders := []*models.Order{
		{
			ID: 1, ClientID: "c1", Instrument: "CLEAN-USD",
			Side: models.SideBuy, Type: models.OrderTypeLimit,
			Status: models.StatusNew, Price: 100, Quantity: 100,
			TimeInForce: "GTC", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
	}
	// Write two snapshots
	wal.WriteSnapshot(dir, 100, orders)
	wal.WriteSnapshot(dir, 200, orders)

	err := wal.CleanOldFiles(dir, 200)
	if err != nil {
		t.Fatalf("CleanOldFiles: %v", err)
	}

	// Should keep only the latest snapshot
	files, _ := filepath.Glob(filepath.Join(dir, "snapshot-*.snap"))
	if len(files) != 1 {
		t.Errorf("expected 1 snapshot after cleanup, got %d", len(files))
	}
}

func TestSnapshotEmptyOrders(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "EMPTY-USD")
	if err := wal.WriteSnapshot(dir, 0, nil); err != nil {
		t.Fatalf("WriteSnapshot with nil: %v", err)
	}
	seqNo, orders, err := wal.LoadSnapshot(dir)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if seqNo != 0 {
		t.Errorf("seqNo = %d", seqNo)
	}
	if len(orders) != 0 {
		t.Errorf("expected 0 orders, got %d", len(orders))
	}
}

func TestLoadSnapshotNoFiles(t *testing.T) {
	dir := t.TempDir()
	seqNo, orders, err := wal.LoadSnapshot(dir)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if seqNo != 0 || len(orders) != 0 {
		t.Error("expected empty result for dir with no snapshots")
	}
}

func TestDecodeRecordTooShort(t *testing.T) {
	_, _, _, err := wal.DecodeRecord([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for too-short record")
	}
}

func TestWriterWithFsyncMode(t *testing.T) {
	dir := t.TempDir()
	w, err := wal.NewWriter(dir, "FSYNC-USD", wal.SyncFsync)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	order := testOrder()
	var buf [512]byte
	seq := w.NextSeqNo()
	n := wal.EncodeOrderAdd(buf[:], seq, order)
	if err := w.Append(buf[:n]); err != nil {
		t.Fatalf("Append with fsync: %v", err)
	}
	w.Close()
}

func TestWriterWithFdatasyncMode(t *testing.T) {
	dir := t.TempDir()
	w, err := wal.NewWriter(dir, "FDS-USD", wal.SyncFdatasync)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	order := testOrder()
	var buf [512]byte
	seq := w.NextSeqNo()
	n := wal.EncodeOrderAdd(buf[:], seq, order)
	if err := w.Append(buf[:n]); err != nil {
		t.Fatalf("Append with fdatasync: %v", err)
	}
	w.Close()
}

func TestReplayAfterSeqNo(t *testing.T) {
	dir := t.TempDir()

	w, err := wal.NewWriter(dir, "SOL-USD", wal.SyncNone)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	order := testOrder()
	order.Instrument = "SOL-USD"
	var buf [512]byte

	// Write 5 events with seqNo 1..5
	for i := 1; i <= 5; i++ {
		order.ID = uint64(i)
		seq := w.NextSeqNo()
		n := wal.EncodeOrderAdd(buf[:], seq, order)
		if err := w.Append(buf[:n]); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	w.Close()

	// Replay only events after seqNo 3
	reader := wal.NewReader(dir, "SOL-USD")
	var count int
	_, err = reader.Replay(3, func(seqNo uint64, eventType uint8, payload []byte) error {
		count++
		if seqNo <= 3 {
			t.Errorf("got seqNo %d, expected > 3", seqNo)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 events after seqNo 3, got %d", count)
	}
}
