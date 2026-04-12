package orderbook_test

import (
	"testing"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
)

func TestAmendQtyDecreasePreservesTimePriority(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	o1 := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(2.0), "c1")
	ob.AddOrder(o1)
	o2 := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "c2")
	ob.AddOrder(o2)

	// Amend o1 qty down (should preserve time priority — o1 still ahead of o2)
	amended, trades, err := ob.AmendOrder(o1.ID, 0, models.FloatToQty(1.0))
	if err != nil {
		t.Fatalf("AmendOrder: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("qty decrease should not produce trades")
	}
	if amended.Quantity != models.FloatToQty(1.0) {
		t.Errorf("qty = %d, want %d", amended.Quantity, models.FloatToQty(1.0))
	}

	// Sell should match o1 first (time priority preserved)
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "seller")
	results, _ := ob.AddOrder(sell)
	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}
	if results[0].Trade.BuyOrderID != o1.ID {
		t.Errorf("should match o1 first (time priority), matched %d", results[0].Trade.BuyOrderID)
	}
}

func TestAmendPriceChangeLosesTimePriority(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	o1 := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "c1")
	ob.AddOrder(o1)
	o2 := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "c2")
	ob.AddOrder(o2)

	// Amend o1: change price away then back (cancel+re-insert loses priority)
	// First move to 50001
	_, _, err := ob.AmendOrder(o1.ID, models.FloatToPrice(50001), 0)
	if err != nil {
		t.Fatalf("AmendOrder to 50001: %v", err)
	}
	// Then move back to 50000
	_, _, err = ob.AmendOrder(o1.ID, models.FloatToPrice(50000), 0)
	if err != nil {
		t.Fatalf("AmendOrder back to 50000: %v", err)
	}

	// Sell should match o2 first (o1 lost time priority)
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "seller")
	results, _ := ob.AddOrder(sell)
	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}
	if results[0].Trade.BuyOrderID != o2.ID {
		t.Errorf("should match o2 first after o1 lost priority, matched %d", results[0].Trade.BuyOrderID)
	}
}

func TestAmendPriceChangeCausesMatch(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "seller")
	ob.AddOrder(sell)

	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "buyer")
	ob.AddOrder(buy)

	// Amend buy price up to cross the sell
	_, trades, err := ob.AmendOrder(buy.ID, models.FloatToPrice(50000), 0)
	if err != nil {
		t.Fatalf("AmendOrder: %v", err)
	}
	if len(trades) != 1 {
		t.Errorf("expected 1 trade from amendment crossing, got %d", len(trades))
	}
}

func TestAmendNotFound(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	_, _, err := ob.AmendOrder(99999, models.FloatToPrice(50000), 0)
	if err == nil {
		t.Error("expected error for nonexistent order")
	}
}

func TestAmendFilledOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "s")
	ob.AddOrder(sell)
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "b")
	ob.AddOrder(buy)
	// sell is now filled

	_, _, err := ob.AmendOrder(sell.ID, 0, models.FloatToQty(2.0))
	if err == nil {
		t.Error("expected error amending filled order")
	}
}

func TestAmendStopOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "stop")
	ob.AddOrder(stop)

	_, _, err := ob.AmendOrder(stop.ID, models.FloatToPrice(52000), 0)
	if err == nil {
		t.Error("expected error amending stop order")
	}
}

func TestAmendBelowFilledQty(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(2.0), "s")
	ob.AddOrder(sell)

	// Partially fill: buy 1.0
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "b")
	ob.AddOrder(buy)
	// sell is now partially filled with 1.0

	// Try to amend qty to 0.5 (below filled 1.0)
	_, _, err := ob.AmendOrder(sell.ID, 0, models.FloatToQty(0.5))
	if err == nil {
		t.Error("expected error reducing below filled qty")
	}
}

func TestAmendNoOp(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	order := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "c")
	ob.AddOrder(order)

	amended, trades, err := ob.AmendOrder(order.ID, models.FloatToPrice(50000), models.FloatToQty(1.0))
	if err != nil {
		t.Fatalf("no-op amend: %v", err)
	}
	if len(trades) != 0 {
		t.Error("no-op should produce no trades")
	}
	if amended.ID != order.ID {
		t.Error("should return same order")
	}
}

func TestAmendQtyIncreaseLosesPriority(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	o1 := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "c1")
	ob.AddOrder(o1)
	o2 := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "c2")
	ob.AddOrder(o2)

	// Increase o1 qty — should lose time priority
	_, _, err := ob.AmendOrder(o1.ID, 0, models.FloatToQty(2.0))
	if err != nil {
		t.Fatalf("AmendOrder: %v", err)
	}

	// Buy should match o2 first
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "buyer")
	results, _ := ob.AddOrder(buy)
	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}
	if results[0].Trade.SellOrderID != o2.ID {
		t.Errorf("should match o2 first, matched %d", results[0].Trade.SellOrderID)
	}
}

func TestAmendMarketOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	mkt := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeMarket,
		0, 0, models.FloatToQty(1.0), "mkt")
	ob.AddOrder(mkt) // market orders don't rest, so this won't find it

	_, _, err := ob.AmendOrder(mkt.ID, models.FloatToPrice(50000), 0)
	if err == nil {
		t.Error("expected error amending non-resting order")
	}
}
