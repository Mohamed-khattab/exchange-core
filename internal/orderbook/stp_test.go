package orderbook_test

import (
	"testing"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
)

func newSTPBook(mode models.STPMode) *orderbook.OrderBook {
	ob := orderbook.NewOrderBook("STP-TEST")
	ob.SetSTP(true, mode)
	return ob
}

func TestSTPDisabledSelfTradeProceeds(t *testing.T) {
	ob := orderbook.NewOrderBook("STP-OFF")
	// STP not enabled, so self-trade should proceed

	sell := models.NewOrder("STP-OFF", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "same-client")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-OFF", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "same-client")
	results, _ := ob.AddOrder(buy)

	if len(results) != 1 {
		t.Errorf("expected 1 trade (STP disabled), got %d", len(results))
	}
	if buy.Status != models.StatusFilled {
		t.Errorf("buy status = %s, want FILLED", buy.Status)
	}
}

func TestSTPCancelResting(t *testing.T) {
	ob := newSTPBook(models.STPCancelResting)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(buy)

	// No trade should occur; resting cancelled, incoming rests
	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	if sell.Status != models.StatusSTPCancelled {
		t.Errorf("sell status = %s, want STP_CANCELLED", sell.Status)
	}
	// Buy should rest in the book since there's nothing to match against now
	_, exists := ob.GetOrder(buy.ID)
	if !exists {
		t.Error("buy should be resting in book after STP cancel of resting")
	}
}

func TestSTPCancelIncoming(t *testing.T) {
	ob := newSTPBook(models.STPCancelIncoming)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(buy)

	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	if buy.Status != models.StatusSTPCancelled {
		t.Errorf("buy status = %s, want STP_CANCELLED", buy.Status)
	}
	// Resting sell should still be in book
	_, exists := ob.GetOrder(sell.ID)
	if !exists {
		t.Error("sell should still be resting in book")
	}
}

func TestSTPCancelBoth(t *testing.T) {
	ob := newSTPBook(models.STPCancelBoth)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(buy)

	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	if buy.Status != models.StatusSTPCancelled {
		t.Errorf("buy status = %s, want STP_CANCELLED", buy.Status)
	}
	if sell.Status != models.StatusSTPCancelled {
		t.Errorf("sell status = %s, want STP_CANCELLED", sell.Status)
	}
}

func TestSTPDifferentClientsNoAction(t *testing.T) {
	ob := newSTPBook(models.STPCancelBoth)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-B")
	results, _ := ob.AddOrder(buy)

	if len(results) != 1 {
		t.Errorf("expected 1 trade (different clients), got %d", len(results))
	}
}

func TestSTPCancelRestingSweepsNextLevel(t *testing.T) {
	ob := newSTPBook(models.STPCancelResting)

	// Resting: same-client sell at 100, different-client sell at 100
	sell1 := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell1)

	sell2 := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-B")
	ob.AddOrder(sell2)

	// Buy from client-A: should skip sell1 (STP cancel resting), match sell2
	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(buy)

	if sell1.Status != models.StatusSTPCancelled {
		t.Errorf("sell1 status = %s, want STP_CANCELLED", sell1.Status)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 trade after STP skip, got %d", len(results))
	}
	if buy.Status != models.StatusFilled {
		t.Errorf("buy status = %s, want FILLED", buy.Status)
	}
}

func TestSTPWithMarketOrder(t *testing.T) {
	ob := newSTPBook(models.STPCancelIncoming)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	mkt := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeMarket,
		0, 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(mkt)

	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	if mkt.Status != models.StatusSTPCancelled {
		t.Errorf("market status = %s, want STP_CANCELLED", mkt.Status)
	}
}

func TestSTPWithIOCOrder(t *testing.T) {
	ob := newSTPBook(models.STPCancelIncoming)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	ioc := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeIOC,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(ioc)

	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	if ioc.Status != models.StatusSTPCancelled {
		t.Errorf("IOC status = %s, want STP_CANCELLED", ioc.Status)
	}
}

func TestSTPPerOrderOverride(t *testing.T) {
	// Default is CANCEL_INCOMING, but order overrides to CANCEL_RESTING
	ob := newSTPBook(models.STPCancelIncoming)

	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	buy.STPMode = models.STPCancelResting // override
	results, _ := ob.AddOrder(buy)

	// With CANCEL_RESTING override: sell should be cancelled, buy rests
	if sell.Status != models.StatusSTPCancelled {
		t.Errorf("sell status = %s, want STP_CANCELLED", sell.Status)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 trades, got %d", len(results))
	}
	_, exists := ob.GetOrder(buy.ID)
	if !exists {
		t.Error("buy should be resting in book")
	}
}

func TestSTPEmptyClientIDNoAction(t *testing.T) {
	ob := newSTPBook(models.STPCancelBoth)

	// Orders with empty ClientID should never trigger STP
	sell := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "")
	ob.AddOrder(sell)

	buy := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "")
	results, _ := ob.AddOrder(buy)

	if len(results) != 1 {
		t.Errorf("expected 1 trade (empty ClientID), got %d", len(results))
	}
}

func TestSTPCancelRestingMarketSweeps(t *testing.T) {
	ob := newSTPBook(models.STPCancelResting)

	// Two sells: same client at 100, different client at 101
	sell1 := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(100), 0, models.FloatToQty(1.0), "client-A")
	ob.AddOrder(sell1)

	sell2 := models.NewOrder("STP-TEST", models.SideSell, models.OrderTypeLimit,
		models.FloatToPrice(101), 0, models.FloatToQty(1.0), "client-B")
	ob.AddOrder(sell2)

	// Market buy from client-A: should STP-cancel sell1 at 100, match sell2 at 101
	mkt := models.NewOrder("STP-TEST", models.SideBuy, models.OrderTypeMarket,
		0, 0, models.FloatToQty(1.0), "client-A")
	results, _ := ob.AddOrder(mkt)

	if sell1.Status != models.StatusSTPCancelled {
		t.Errorf("sell1 status = %s, want STP_CANCELLED", sell1.Status)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(results))
	}
	if results[0].Trade.Price != models.FloatToPrice(101) {
		t.Errorf("trade price = %d, want %d (should match sell2)", results[0].Trade.Price, models.FloatToPrice(101))
	}
	if mkt.Status != models.StatusFilled {
		t.Errorf("market status = %s, want FILLED", mkt.Status)
	}
}
