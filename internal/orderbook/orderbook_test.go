package orderbook_test

import (
	"fmt"
	"testing"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
)

// ── Unit Tests ────────────────────────────────────────────────────────────────

func TestLimitOrderMatch(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Add a resting sell at 50000
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "seller-1")
	results, err := ob.AddOrder(sell)
	if err != nil {
		t.Fatalf("AddOrder sell: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}
	if sell.Status != models.StatusNew {
		t.Errorf("expected NEW, got %s", sell.Status)
	}

	// Add a crossing buy
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(0.5), "buyer-1")
	results, err = ob.AddOrder(buy)
	if err != nil {
		t.Fatalf("AddOrder buy: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}

	trade := results[0].Trade
	if trade.Quantity != models.FloatToQty(0.5) {
		t.Errorf("trade qty mismatch: got %d", trade.Quantity)
	}
	if trade.Price != models.FloatToPrice(50000) {
		t.Errorf("trade price mismatch: got %d", trade.Price)
	}
	if buy.Status != models.StatusFilled {
		t.Errorf("buy should be FILLED, got %s", buy.Status)
	}
	if sell.Status != models.StatusPartiallyFilled {
		t.Errorf("sell should be PARTIALLY_FILLED, got %s", sell.Status)
	}
}

func TestMarketOrderNoLiquidity(t *testing.T) {
	ob := orderbook.NewOrderBook("ETH-USD")
	market := models.NewOrder("ETH-USD", models.SideBuy, models.OrderTypeMarket,
		0, 0, models.FloatToQty(1.0), "buyer-mkt")
	_, err := ob.AddOrder(market)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if market.Status != models.StatusRejected {
		t.Errorf("expected REJECTED, got %s", market.Status)
	}
}

func TestFOKFullFill(t *testing.T) {
	ob := orderbook.NewOrderBook("SOL-USD")

	// Add 2 BTC of resting sell liquidity
	for i := 0; i < 4; i++ {
		sell := models.NewOrder("SOL-USD", models.SideSell, models.OrderTypeLimit,
			models.FloatToPrice(100), 0, models.FloatToQty(0.5), fmt.Sprintf("s%d", i))
		_, _ = ob.AddOrder(sell)
	}

	// FOK for 1.5 BTC — should fill (2 BTC available)
	fok := models.NewOrder("SOL-USD", models.SideBuy, models.OrderTypeFOK,
		models.FloatToPrice(100), 0, models.FloatToQty(1.5), "fok-buyer")
	results, err := ob.AddOrder(fok)
	if err != nil {
		t.Fatalf("FOK error: %v", err)
	}
	if fok.Status != models.StatusFilled {
		t.Errorf("FOK should be FILLED, got %s (trades=%d)", fok.Status, len(results))
	}
}

func TestFOKCancelledInsufficientLiquidity(t *testing.T) {
	ob := orderbook.NewOrderBook("BNB-USD")
	sell := models.NewOrder("BNB-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(300), 0, models.FloatToQty(0.1), "small-sell")
	_, _ = ob.AddOrder(sell)

	fok := models.NewOrder("BNB-USD", models.SideBuy, models.OrderTypeFOK,
		models.FloatToPrice(300), 0, models.FloatToQty(1.0), "fok-too-big")
	results, _ := ob.AddOrder(fok)
	if fok.Status != models.StatusCancelled {
		t.Errorf("FOK should be CANCELLED, got %s", fok.Status)
	}
	if len(results) != 0 {
		t.Errorf("FOK cancel should produce 0 trades, got %d", len(results))
	}
}

func TestCancelOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(60000), 0, models.FloatToQty(2.0), "cancel-test")
	_, _ = ob.AddOrder(sell)

	cancelled, ok := ob.CancelOrder(sell.ID)
	if !ok {
		t.Fatal("cancel returned false")
	}
	if cancelled.Status != models.StatusCancelled {
		t.Errorf("expected CANCELLED, got %s", cancelled.Status)
	}
	// Should not exist in book anymore
	_, exists := ob.GetOrder(sell.ID)
	if exists {
		t.Error("cancelled order still in book")
	}
}

// ── Additional Tests ─────────────────────────────────────────────────────────

func TestIOCPartialFill(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(0.3), "s1")
	ob.AddOrder(sell)

	ioc := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeIOC,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "ioc-buyer")
	results, _ := ob.AddOrder(ioc)

	if len(results) != 1 {
		t.Errorf("expected 1 trade, got %d", len(results))
	}
	// IOC should be cancelled because it couldn't fill completely
	if ioc.Status != models.StatusCancelled {
		t.Errorf("IOC should be CANCELLED, got %s", ioc.Status)
	}
	if ioc.FilledQty != models.FloatToQty(0.3) {
		t.Errorf("IOC filledQty = %d, want %d", ioc.FilledQty, models.FloatToQty(0.3))
	}
}

func TestIOCFullFill(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "s1")
	ob.AddOrder(sell)

	ioc := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeIOC,
		models.FloatToPrice(50000), 0, models.FloatToQty(0.5), "ioc-buyer")
	results, _ := ob.AddOrder(ioc)

	if len(results) != 1 {
		t.Errorf("expected 1 trade, got %d", len(results))
	}
	if ioc.Status != models.StatusFilled {
		t.Errorf("IOC should be FILLED, got %s", ioc.Status)
	}
}

func TestMarketSellOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Add resting buy
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(2.0), "buyer")
	ob.AddOrder(buy)

	// Market sell
	mkt := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeMarket,
		0, 0, models.FloatToQty(1.0), "seller")
	results, err := ob.AddOrder(mkt)
	if err != nil {
		t.Fatalf("market sell error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 trade, got %d", len(results))
	}
	if mkt.Status != models.StatusFilled {
		t.Errorf("market sell status = %s, want FILLED", mkt.Status)
	}
}

func TestMarketPartialFill(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(0.5), "buyer")
	ob.AddOrder(buy)

	mkt := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeMarket,
		0, 0, models.FloatToQty(1.0), "seller")
	ob.AddOrder(mkt)

	if mkt.Status != models.StatusPartiallyFilled {
		t.Errorf("expected PARTIALLY_FILLED, got %s", mkt.Status)
	}
}

func TestLimitSellNoCrossing(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Buy at 49000
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "buyer")
	ob.AddOrder(buy)

	// Sell at 50000 -- no crossing
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "seller")
	results, _ := ob.AddOrder(sell)
	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	if sell.Status != models.StatusNew {
		t.Errorf("sell status = %s, want NEW", sell.Status)
	}
}

func TestLimitPartialFillAndRest(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(0.5), "s1")
	ob.AddOrder(sell)

	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "b1")
	results, _ := ob.AddOrder(buy)

	if len(results) != 1 {
		t.Errorf("expected 1 trade, got %d", len(results))
	}
	if buy.Status != models.StatusPartiallyFilled {
		t.Errorf("buy status = %s, want PARTIALLY_FILLED", buy.Status)
	}
	// Remaining 0.5 should be in the book
	_, ok := ob.GetOrder(buy.ID)
	if !ok {
		t.Error("buy order should still be in book")
	}
}

func TestFOKSellSide(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Add resting bids
	for i := 0; i < 3; i++ {
		bid := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
			models.FloatToPrice(48000), 0, models.FloatToQty(1.0), fmt.Sprintf("b%d", i))
		ob.AddOrder(bid)
	}

	// FOK sell for 2.0 -- should fill
	fok := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeFOK,
		models.FloatToPrice(48000), 0, models.FloatToQty(2.0), "fok-seller")
	results, _ := ob.AddOrder(fok)
	if fok.Status != models.StatusFilled {
		t.Errorf("FOK sell status = %s, want FILLED", fok.Status)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 trades, got %d", len(results))
	}
}

func TestUnsupportedOrderType(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	order := models.NewOrder("BTC-USD", models.SideBuy, models.OrderType("INVALID_TYPE"),
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "bad")
	_, err := ob.AddOrder(order)
	if err == nil {
		t.Error("expected error for unsupported order type")
	}
}

func TestSnapshot(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	for i := 0; i < 3; i++ {
		sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
			models.FloatToPrice(50000+float64(i)*100), 0, models.FloatToQty(1.0), fmt.Sprintf("s%d", i))
		ob.AddOrder(sell)
	}
	for i := 0; i < 2; i++ {
		buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
			models.FloatToPrice(49000-float64(i)*100), 0, models.FloatToQty(1.0), fmt.Sprintf("b%d", i))
		ob.AddOrder(buy)
	}

	snap := ob.Snapshot(10)
	if snap.Instrument != "BTC-USD" {
		t.Errorf("Instrument = %s", snap.Instrument)
	}
	if len(snap.Asks) != 3 {
		t.Errorf("expected 3 ask levels, got %d", len(snap.Asks))
	}
	if len(snap.Bids) != 2 {
		t.Errorf("expected 2 bid levels, got %d", len(snap.Bids))
	}
}

func TestSnapshotDepthLimit(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	for i := 0; i < 10; i++ {
		sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
			models.FloatToPrice(50000+float64(i)*100), 0, models.FloatToQty(1.0), fmt.Sprintf("s%d", i))
		ob.AddOrder(sell)
	}

	snap := ob.Snapshot(3)
	if len(snap.Asks) != 3 {
		t.Errorf("expected 3 ask levels with depth=3, got %d", len(snap.Asks))
	}
}

func TestStats(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "s1")
	ob.AddOrder(sell)
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "b1")
	ob.AddOrder(buy)

	bidLevels, askLevels, openOrders, bestBid, bestAsk, _ := ob.Stats()
	if bidLevels != 1 {
		t.Errorf("bidLevels = %d", bidLevels)
	}
	if askLevels != 1 {
		t.Errorf("askLevels = %d", askLevels)
	}
	if openOrders != 2 {
		t.Errorf("openOrders = %d", openOrders)
	}
	if bestBid != models.FloatToPrice(49000) {
		t.Errorf("bestBid = %d", bestBid)
	}
	if bestAsk != models.FloatToPrice(50000) {
		t.Errorf("bestAsk = %d", bestAsk)
	}
}

func TestStatsEmptyBook(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	bidLevels, askLevels, openOrders, bestBid, bestAsk, last := ob.Stats()
	if bidLevels != 0 || askLevels != 0 || openOrders != 0 {
		t.Error("empty book should have zero stats")
	}
	if bestBid != 0 || bestAsk != 0 || last != 0 {
		t.Error("empty book should have zero prices")
	}
}

func TestAllOrders(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "s1")
	ob.AddOrder(sell)
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "b1")
	ob.AddOrder(buy)

	all := ob.AllOrders()
	if len(all) != 2 {
		t.Errorf("expected 2 orders, got %d", len(all))
	}
}

func TestRestoreOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	order := &models.Order{
		ID: 999, ClientID: "restored", Instrument: "BTC-USD",
		Side: models.SideBuy, Type: models.OrderTypeLimit,
		Status: models.StatusNew, Price: models.FloatToPrice(48000),
		Quantity: models.FloatToQty(1.0), TimeInForce: "GTC",
	}
	ob.RestoreOrder(order)

	got, ok := ob.GetOrder(999)
	if !ok {
		t.Fatal("restored order not found")
	}
	if got.ClientID != "restored" {
		t.Errorf("ClientID = %s", got.ClientID)
	}
}

func TestCancelNonexistentOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	_, ok := ob.CancelOrder(12345)
	if ok {
		t.Error("cancel of nonexistent order should return false")
	}
}

func TestMultipleFillsAtSamePrice(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// 3 sells at same price
	for i := 0; i < 3; i++ {
		sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
			models.FloatToPrice(50000), 0, models.FloatToQty(1.0), fmt.Sprintf("s%d", i))
		ob.AddOrder(sell)
	}

	// Big buy that sweeps all 3
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(3.0), "big-buyer")
	results, _ := ob.AddOrder(buy)

	if len(results) != 3 {
		t.Errorf("expected 3 trades, got %d", len(results))
	}
	if buy.Status != models.StatusFilled {
		t.Errorf("buy status = %s, want FILLED", buy.Status)
	}
}

func TestPriceTimePriority(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Two sells at same price; first should fill first
	sell1 := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "first")
	ob.AddOrder(sell1)
	sell2 := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "second")
	ob.AddOrder(sell2)

	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "buyer")
	results, _ := ob.AddOrder(buy)

	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}
	if results[0].Trade.SellOrderID != sell1.ID {
		t.Errorf("trade matched sell %d, expected %d (time priority)", results[0].Trade.SellOrderID, sell1.ID)
	}
	// sell1 should be filled, sell2 still resting
	if sell1.Status != models.StatusFilled {
		t.Errorf("sell1 status = %s", sell1.Status)
	}
	_, exists := ob.GetOrder(sell2.ID)
	if !exists {
		t.Error("sell2 should still be in book")
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

// BenchmarkLimitOrderMatch measures pure matching throughput.
// Run with: go test ./internal/orderbook/ -bench=. -benchtime=5s
func BenchmarkLimitOrderMatch(b *testing.B) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Pre-seed 1000 resting sell orders across 100 price levels
	for i := 0; i < 1000; i++ {
		price := models.FloatToPrice(50000 + float64(i%100)*0.01)
		sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
			price, 0, models.FloatToQty(1.0), fmt.Sprintf("seed-%d", i))
		_, _ = ob.AddOrder(sell)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeMarket,
			0, 0, models.FloatToQty(0.01), fmt.Sprintf("bench-%d", i))
		_, _ = ob.AddOrder(buy)
	}
}

func BenchmarkAddLimitOrder(b *testing.B) {
	ob := orderbook.NewOrderBook("BTC-USD")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		price := models.FloatToPrice(50000 - float64(i%500)*0.01)
		order := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
			price, 0, models.FloatToQty(0.1), fmt.Sprintf("b-%d", i))
		_, _ = ob.AddOrder(order)
	}
}
