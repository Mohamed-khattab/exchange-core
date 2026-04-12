package orderbook_test

import (
	"testing"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
)

func TestMassCancelAllOrders(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	for i := 0; i < 5; i++ {
		ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
			models.FloatToPrice(49000+float64(i)*100), 0, models.FloatToQty(1.0), "c1"))
	}
	for i := 0; i < 3; i++ {
		ob.AddOrder(models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
			models.FloatToPrice(51000+float64(i)*100), 0, models.FloatToQty(1.0), "c2"))
	}

	cancelled := ob.MassCancel(models.MassCancelFilter{Instrument: "BTC-USD"})
	if len(cancelled) != 8 {
		t.Errorf("expected 8 cancelled, got %d", len(cancelled))
	}

	all := ob.AllOrders()
	if len(all) != 0 {
		t.Errorf("expected 0 orders remaining, got %d", len(all))
	}
}

func TestMassCancelByClientID(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "alice"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(48000), 0, models.FloatToQty(1.0), "bob"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "alice"))

	cancelled := ob.MassCancel(models.MassCancelFilter{
		Instrument: "BTC-USD",
		ClientID:   "alice",
	})
	if len(cancelled) != 2 {
		t.Errorf("expected 2 cancelled (alice's orders), got %d", len(cancelled))
	}

	all := ob.AllOrders()
	if len(all) != 1 {
		t.Errorf("expected 1 order remaining (bob's), got %d", len(all))
	}
}

func TestMassCancelBySide(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "c1"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(48000), 0, models.FloatToQty(1.0), "c2"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "c3"))

	side := models.SideBuy
	cancelled := ob.MassCancel(models.MassCancelFilter{
		Instrument: "BTC-USD",
		Side:       &side,
	})
	if len(cancelled) != 2 {
		t.Errorf("expected 2 cancelled (buys), got %d", len(cancelled))
	}

	all := ob.AllOrders()
	if len(all) != 1 {
		t.Errorf("expected 1 order remaining (sell), got %d", len(all))
	}
}

func TestMassCancelIncludesStopOrders(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "lim"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeStop,
		0, models.FloatToPrice(51000), models.FloatToQty(1.0), "stop"))

	cancelled := ob.MassCancel(models.MassCancelFilter{Instrument: "BTC-USD"})
	if len(cancelled) != 2 {
		t.Errorf("expected 2 cancelled (limit + stop), got %d", len(cancelled))
	}
}

func TestMassCancelEmptyBook(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")
	cancelled := ob.MassCancel(models.MassCancelFilter{Instrument: "BTC-USD"})
	if len(cancelled) != 0 {
		t.Errorf("expected 0, got %d", len(cancelled))
	}
}

func TestMassCancelNoMatches(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "alice"))

	cancelled := ob.MassCancel(models.MassCancelFilter{
		Instrument: "BTC-USD",
		ClientID:   "nonexistent",
	})
	if len(cancelled) != 0 {
		t.Errorf("expected 0, got %d", len(cancelled))
	}
}

func TestMassCancelCombinedFilter(t *testing.T) {
	ob := orderbook.NewOrderBook("BTC-USD")

	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(49000), 0, models.FloatToQty(1.0), "alice"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(51000), 0, models.FloatToQty(1.0), "alice"))
	ob.AddOrder(models.NewOrder("BTC-USD", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(48000), 0, models.FloatToQty(1.0), "bob"))

	side := models.SideBuy
	cancelled := ob.MassCancel(models.MassCancelFilter{
		Instrument: "BTC-USD",
		ClientID:   "alice",
		Side:       &side,
	})
	// Should only cancel alice's buy, not her sell or bob's buy
	if len(cancelled) != 1 {
		t.Errorf("expected 1 cancelled (alice buy only), got %d", len(cancelled))
	}

	all := ob.AllOrders()
	if len(all) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(all))
	}
}
