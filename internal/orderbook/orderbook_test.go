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
