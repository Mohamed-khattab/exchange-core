package engine

import (
	"os"
	"testing"

	"github.com/trading/matching-engine/internal/metrics"
	"github.com/trading/matching-engine/internal/models"
)

func newTestEngine(instruments []string) *MatchingEngine {
	mc := metrics.NewCollector()
	me := NewMatchingEngine(instruments, mc)
	me.Start()
	return me
}

func TestSubmitAndMatchLimitOrders(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	// Place a resting sell
	_, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "seller", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 50000, Quantity: 1.0,
	})
	if err != nil {
		t.Fatalf("sell order: %v", err)
	}

	// Place a crossing buy
	order, trades, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "buyer", Instrument: "BTC-USD", Side: "BUY",
		Type: "LIMIT", Price: 50000, Quantity: 0.5,
	})
	if err != nil {
		t.Fatalf("buy order: %v", err)
	}
	if order.Status != models.StatusFilled {
		t.Errorf("buy status = %s, want FILLED", order.Status)
	}
	if len(trades) != 1 {
		t.Errorf("expected 1 trade, got %d", len(trades))
	}
}

func TestSubmitMarketOrder(t *testing.T) {
	me := newTestEngine([]string{"ETH-USD"})
	defer me.Stop()

	// No liquidity -- market should be rejected
	order, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "mkt", Instrument: "ETH-USD", Side: "BUY",
		Type: "MARKET", Quantity: 1.0,
	})
	if err != nil {
		t.Fatalf("market order error: %v", err)
	}
	if order.Status != models.StatusRejected {
		t.Errorf("status = %s, want REJECTED", order.Status)
	}
}

func TestSubmitIOCOrder(t *testing.T) {
	me := newTestEngine([]string{"SOL-USD"})
	defer me.Stop()

	// IOC with no liquidity -- should be cancelled
	order, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "ioc", Instrument: "SOL-USD", Side: "BUY",
		Type: "IOC", Price: 100, Quantity: 1.0,
	})
	if err != nil {
		t.Fatalf("IOC error: %v", err)
	}
	if order.Status != models.StatusCancelled {
		t.Errorf("IOC status = %s, want CANCELLED", order.Status)
	}
}

func TestCancelOrder(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	order, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "c1", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 60000, Quantity: 1.0,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	cancelled, err := me.CancelOrder("BTC-USD", order.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != models.StatusCancelled {
		t.Errorf("status = %s, want CANCELLED", cancelled.Status)
	}
}

func TestCancelNonexistentOrder(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, err := me.CancelOrder("BTC-USD", 999999)
	if err == nil {
		t.Error("expected error for nonexistent order")
	}
}

func TestUnknownInstrument(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "c1", Instrument: "DOGE-USD", Side: "BUY",
		Type: "LIMIT", Price: 1, Quantity: 100,
	})
	if err == nil {
		t.Error("expected error for unknown instrument")
	}
}

func TestInvalidSide(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "c1", Instrument: "BTC-USD", Side: "INVALID",
		Type: "LIMIT", Price: 50000, Quantity: 1,
	})
	if err == nil {
		t.Error("expected error for invalid side")
	}
}

func TestGetOrderBook(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	me.SubmitOrder(&models.OrderRequest{
		ClientID: "s1", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 50000, Quantity: 1.0,
	})
	me.SubmitOrder(&models.OrderRequest{
		ClientID: "b1", Instrument: "BTC-USD", Side: "BUY",
		Type: "LIMIT", Price: 49000, Quantity: 2.0,
	})

	snap, err := me.GetOrderBook("BTC-USD", 10)
	if err != nil {
		t.Fatalf("GetOrderBook: %v", err)
	}
	if snap.Instrument != "BTC-USD" {
		t.Errorf("Instrument = %s", snap.Instrument)
	}
	if len(snap.Bids) != 1 {
		t.Errorf("expected 1 bid level, got %d", len(snap.Bids))
	}
	if len(snap.Asks) != 1 {
		t.Errorf("expected 1 ask level, got %d", len(snap.Asks))
	}
}

func TestGetOrderBookUnknownInstrument(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, err := me.GetOrderBook("FAKE-USD", 10)
	if err == nil {
		t.Error("expected error for unknown instrument")
	}
}

func TestGetOrder(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	order, _, _ := me.SubmitOrder(&models.OrderRequest{
		ClientID: "c1", Instrument: "BTC-USD", Side: "BUY",
		Type: "LIMIT", Price: 45000, Quantity: 1.0,
	})

	got, err := me.GetOrder("BTC-USD", order.ID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got.ID != order.ID {
		t.Errorf("ID mismatch: %d != %d", got.ID, order.ID)
	}
}

func TestGetOrderNotFound(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, err := me.GetOrder("BTC-USD", 12345)
	if err == nil {
		t.Error("expected error for nonexistent order")
	}
}

func TestGetBookStats(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	me.SubmitOrder(&models.OrderRequest{
		ClientID: "s1", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 50000, Quantity: 1.0,
	})

	stats, err := me.GetBookStats("BTC-USD")
	if err != nil {
		t.Fatalf("GetBookStats: %v", err)
	}
	if stats["instrument"] != "BTC-USD" {
		t.Errorf("instrument = %v", stats["instrument"])
	}
	if stats["ask_levels"].(int) != 1 {
		t.Errorf("ask_levels = %v", stats["ask_levels"])
	}
}

func TestGetBookStatsUnknown(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, err := me.GetBookStats("NOPE-USD")
	if err == nil {
		t.Error("expected error")
	}
}

func TestListInstruments(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD", "ETH-USD"})
	defer me.Stop()

	list := me.ListInstruments()
	if len(list) != 2 {
		t.Errorf("expected 2 instruments, got %d", len(list))
	}
}

func TestCancelOrderUnknownInstrument(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, err := me.CancelOrder("UNKNOWN", 1)
	if err == nil {
		t.Error("expected error for unknown instrument")
	}
}

func TestGetOrderUnknownInstrument(t *testing.T) {
	me := newTestEngine([]string{"BTC-USD"})
	defer me.Stop()

	_, err := me.GetOrder("UNKNOWN", 1)
	if err == nil {
		t.Error("expected error for unknown instrument")
	}
}

// ── WAL Integration Tests ────────────────────────────────────────────────────

func TestEngineWithWAL(t *testing.T) {
	dir := t.TempDir()
	mc := metrics.NewCollector()
	cfg := EngineConfig{WAL: WALConfig{
		Enabled:       true,
		Dir:           dir,
		SyncMode:      "none",
		SnapshotEvery: 0,
	}}

	me := NewMatchingEngine([]string{"BTC-USD"}, mc, cfg)
	me.Start()

	order, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "w1", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 50000, Quantity: 1.0,
	})
	if err != nil {
		t.Fatalf("submit with WAL: %v", err)
	}
	if order.Status != models.StatusNew {
		t.Errorf("status = %s, want NEW", order.Status)
	}

	me.Stop()

	// Verify WAL files were created
	entries, _ := os.ReadDir(dir + "/BTC-USD")
	walFiles := 0
	for _, e := range entries {
		if !e.IsDir() {
			walFiles++
		}
	}
	if walFiles == 0 {
		t.Error("expected WAL files to be created")
	}
}

func TestEngineWALRecovery(t *testing.T) {
	dir := t.TempDir()
	mc := metrics.NewCollector()
	cfg := EngineConfig{WAL: WALConfig{
		Enabled:       true,
		Dir:           dir,
		SyncMode:      "none",
		SnapshotEvery: 0,
	}}

	// First run: submit orders
	me1 := NewMatchingEngine([]string{"BTC-USD"}, mc, cfg)
	me1.Start()

	me1.SubmitOrder(&models.OrderRequest{
		ClientID: "s1", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 50000, Quantity: 2.0,
	})
	me1.SubmitOrder(&models.OrderRequest{
		ClientID: "b1", Instrument: "BTC-USD", Side: "BUY",
		Type: "LIMIT", Price: 49000, Quantity: 1.0,
	})

	me1.Stop()

	// Second run: recover from WAL
	mc2 := metrics.NewCollector()
	me2 := NewMatchingEngine([]string{"BTC-USD"}, mc2, cfg)
	me2.Start()

	snap, err := me2.GetOrderBook("BTC-USD", 10)
	if err != nil {
		t.Fatalf("GetOrderBook after recovery: %v", err)
	}

	// Should have 1 ask level (sell at 50000, partially or fully resting)
	// and 1 bid level (buy at 49000)
	if len(snap.Asks) == 0 && len(snap.Bids) == 0 {
		t.Error("expected recovered order book to have resting orders")
	}

	me2.Stop()
}

func TestEngineWALCancelRecovery(t *testing.T) {
	dir := t.TempDir()
	mc := metrics.NewCollector()
	cfg := EngineConfig{WAL: WALConfig{
		Enabled:       true,
		Dir:           dir,
		SyncMode:      "none",
		SnapshotEvery: 0,
	}}

	// First run: submit and then cancel
	me1 := NewMatchingEngine([]string{"BTC-USD"}, mc, cfg)
	me1.Start()

	order, _, _ := me1.SubmitOrder(&models.OrderRequest{
		ClientID: "c1", Instrument: "BTC-USD", Side: "SELL",
		Type: "LIMIT", Price: 60000, Quantity: 1.0,
	})
	me1.CancelOrder("BTC-USD", order.ID)
	me1.Stop()

	// Second run: book should be empty after replay
	mc2 := metrics.NewCollector()
	me2 := NewMatchingEngine([]string{"BTC-USD"}, mc2, cfg)
	me2.Start()

	snap, _ := me2.GetOrderBook("BTC-USD", 10)
	if len(snap.Asks) != 0 {
		t.Errorf("expected 0 ask levels after cancel recovery, got %d", len(snap.Asks))
	}

	me2.Stop()
}

func TestEngineWALSnapshot(t *testing.T) {
	dir := t.TempDir()
	mc := metrics.NewCollector()
	cfg := EngineConfig{WAL: WALConfig{
		Enabled:       true,
		Dir:           dir,
		SyncMode:      "none",
		SnapshotEvery: 3, // snapshot every 3 events
	}}

	me := NewMatchingEngine([]string{"BTC-USD"}, mc, cfg)
	me.Start()

	// Submit 4 orders to trigger a snapshot (3 = threshold)
	for i := 0; i < 4; i++ {
		me.SubmitOrder(&models.OrderRequest{
			ClientID: "s", Instrument: "BTC-USD", Side: "SELL",
			Type: "LIMIT", Price: 60000 + float64(i), Quantity: 1.0,
		})
	}

	me.Stop()

	// Verify snapshot was created
	files, _ := os.ReadDir(dir + "/BTC-USD")
	hasSnapshot := false
	for _, f := range files {
		if len(f.Name()) > 8 && f.Name()[:8] == "snapshot" {
			hasSnapshot = true
		}
	}
	if !hasSnapshot {
		t.Error("expected snapshot file to be created after 3 events")
	}
}

func TestEngineNoWAL(t *testing.T) {
	mc := metrics.NewCollector()
	me := NewMatchingEngine([]string{"BTC-USD"}, mc)
	me.Start()
	defer me.Stop()

	order, _, err := me.SubmitOrder(&models.OrderRequest{
		ClientID: "c1", Instrument: "BTC-USD", Side: "BUY",
		Type: "LIMIT", Price: 50000, Quantity: 1.0,
	})
	if err != nil {
		t.Fatalf("submit without WAL: %v", err)
	}
	if order.Status != models.StatusNew {
		t.Errorf("status = %s", order.Status)
	}
}

func TestParseSide(t *testing.T) {
	buy, err := parseSide("BUY")
	if err != nil || buy != models.SideBuy {
		t.Errorf("parseSide(BUY) = %v, %v", buy, err)
	}

	sell, err := parseSide("SELL")
	if err != nil || sell != models.SideSell {
		t.Errorf("parseSide(SELL) = %v, %v", sell, err)
	}

	_, err = parseSide("INVALID")
	if err == nil {
		t.Error("expected error for INVALID side")
	}
}
