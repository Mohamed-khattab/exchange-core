package orderbook_test

import (
	"testing"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
)

func TestStopOrderAdded(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "stop-buyer")
	results, err := ob.AddOrder(stop, 0)

	if err != nil {
		t.Fatalf("AddOrder stop: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("stop should produce no trades, got %d", len(results))
	}
	if stop.Status != models.StatusPendingTrigger {
		t.Errorf("stop status = %s, want PENDING_TRIGGER", stop.Status)
	}
}

func TestStopLimitOrderAdded(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStopLimit,
		models.FloatToPrice(51500), models.FloatToPrice(51000), models.FloatToQty(1.0), "sl-buyer")
	results, err := ob.AddOrder(stop, 0)

	if err != nil {
		t.Fatalf("AddOrder stop-limit: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("stop-limit should produce no trades")
	}
	if stop.Status != models.StatusPendingTrigger {
		t.Errorf("status = %s, want PENDING_TRIGGER", stop.Status)
	}
}

func TestBuyStopTriggersWhenPriceRises(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Add a buy stop at 51000
	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "stop-buyer")
	ob.AddOrder(stop, 0)

	// Add a resting sell at 51000
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "seller")
	ob.AddOrder(sell, 0)

	// Buy at 51000 to create a trade that moves lastPrice to 51000
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "buyer")
	results, _ := ob.AddOrder(buy, 0)

	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}

	// Now check stops -- should trigger the buy stop
	triggered := ob.CheckStops()
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered stop, got %d", len(triggered))
	}
	if triggered[0].ID != stop.ID {
		t.Errorf("triggered order ID = %d, want %d", triggered[0].ID, stop.ID)
	}
}

func TestSellStopTriggersWhenPriceDrops(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Add a sell stop at 49000
	stop := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeStop,
		0, models.FloatToPrice(49000), models.FloatToQty(1.0), "stop-seller")
	ob.AddOrder(stop, 0)

	// Add a resting buy at 49000
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "buyer")
	ob.AddOrder(buy, 0)

	// Sell at 49000 to move lastPrice down
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "seller")
	results, _ := ob.AddOrder(sell, 0)

	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}

	triggered := ob.CheckStops()
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered stop, got %d", len(triggered))
	}
	if triggered[0].ID != stop.ID {
		t.Errorf("triggered order ID = %d, want %d", triggered[0].ID, stop.ID)
	}
}

func TestStopNotTriggeredWhenPriceNotCrossed(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Buy stop at 55000
	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(55000), models.FloatToQty(1.0), "stop-buyer")
	ob.AddOrder(stop, 0)

	// Trade at 50000 (below stop price)
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "seller")
	ob.AddOrder(sell, 0)
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "buyer")
	ob.AddOrder(buy, 0)

	triggered := ob.CheckStops()
	if len(triggered) != 0 {
		t.Errorf("expected 0 triggered stops, got %d", len(triggered))
	}
}

func TestMultipleStopsSamePrice(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	s1 := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "s1")
	ob.AddOrder(s1, 0)
	s2 := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(2.0), "s2")
	ob.AddOrder(s2, 0)

	// Create a trade at 51000
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "seller")
	ob.AddOrder(sell, 0)
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "buyer")
	ob.AddOrder(buy, 0)

	triggered := ob.CheckStops()
	if len(triggered) != 2 {
		t.Fatalf("expected 2 triggered stops, got %d", len(triggered))
	}
}

func TestCancelPendingStop(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "stop")
	ob.AddOrder(stop, 0)

	cancelled, ok := ob.CancelOrder(stop.ID)
	if !ok {
		t.Fatal("cancel should succeed")
	}
	if cancelled.Status != models.StatusCancelled {
		t.Errorf("status = %s, want CANCELLED", cancelled.Status)
	}

	// Should not trigger after cancellation
	triggered := ob.CheckStops()
	if len(triggered) != 0 {
		t.Errorf("cancelled stop should not trigger, got %d", len(triggered))
	}
}

func TestStopIncludedInAllOrders(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	limit := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(50000), 0, models.FloatToQty(1.0), "lim")
	ob.AddOrder(limit, 0)

	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "stop")
	ob.AddOrder(stop, 0)

	all := ob.AllOrders()
	if len(all) != 2 {
		t.Errorf("expected 2 orders (1 resting + 1 stop), got %d", len(all))
	}
}

func TestRestoreStopOrder(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	stop := &models.Order{
		ID: 999, ClientID: "restored", Instrument: "BTC-USD",
		Side: models.SideBuy, Type: models.OrderTypeStop,
		Status: models.StatusPendingTrigger,
		StopPrice: models.FloatToPrice(52000),
		Quantity:  models.FloatToQty(1.0),
	}
	ob.RestoreOrder(stop)

	all := ob.AllOrders()
	if len(all) != 1 {
		t.Fatalf("expected 1 order, got %d", len(all))
	}
	if all[0].ID != 999 {
		t.Errorf("restored order ID = %d", all[0].ID)
	}
}

func TestStopLimitActivatesAsLimit(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	// Stop-limit: trigger at 51000, limit price 51500
	stop := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStopLimit,
		models.FloatToPrice(51500), models.FloatToPrice(51000), models.FloatToQty(1.0), "sl")
	ob.AddOrder(stop, 0)

	// Trade at 51000 to trigger
	sell := models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "s")
	ob.AddOrder(sell, 0)
	buy := models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "b")
	ob.AddOrder(buy, 0)

	triggered := ob.CheckStops()
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered, got %d", len(triggered))
	}

	// Verify it keeps its limit price
	if triggered[0].Price != models.FloatToPrice(51500) {
		t.Errorf("activated order price = %d, want %d", triggered[0].Price, models.FloatToPrice(51500))
	}
}

func TestCheckStopsEmptyBook(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	triggered := ob.CheckStops()
	if len(triggered) != 0 {
		t.Errorf("expected 0 triggered, got %d", len(triggered))
	}
}
