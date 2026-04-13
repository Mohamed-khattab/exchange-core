package models

import (
	"strings"
	"sync"
	"testing"
)

// ── Side ─────────────────────────────────────────────────────────────────────

func TestSideString(t *testing.T) {
	if SideBuy.String() != "BUY" {
		t.Errorf("SideBuy.String() = %s, want BUY", SideBuy.String())
	}
	if SideSell.String() != "SELL" {
		t.Errorf("SideSell.String() = %s, want SELL", SideSell.String())
	}
}

func TestSideOpposite(t *testing.T) {
	if SideBuy.Opposite() != SideSell {
		t.Error("SideBuy.Opposite() should be SideSell")
	}
	if SideSell.Opposite() != SideBuy {
		t.Error("SideSell.Opposite() should be SideBuy")
	}
}

// ── Order ────────────────────────────────────────────────────────────────────

func TestNewOrder(t *testing.T) {
	o := NewOrder("BTC-USD", SideBuy, OrderTypeLimit, 5000000000000, 0, 100000000, "client-1")

	if o.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if o.ClientID != "client-1" {
		t.Errorf("ClientID = %s, want client-1", o.ClientID)
	}
	if o.Instrument != "BTC-USD" {
		t.Errorf("Instrument = %s", o.Instrument)
	}
	if o.Side != SideBuy {
		t.Errorf("Side = %d", o.Side)
	}
	if o.Type != OrderTypeLimit {
		t.Errorf("Type = %s", o.Type)
	}
	if o.Status != StatusNew {
		t.Errorf("Status = %s, want NEW", o.Status)
	}
	if o.Price != 5000000000000 {
		t.Errorf("Price = %d", o.Price)
	}
	if o.Quantity != 100000000 {
		t.Errorf("Quantity = %d", o.Quantity)
	}
	if o.TimeInForce != "GTC" {
		t.Errorf("TimeInForce = %s", o.TimeInForce)
	}
	if o.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	if o.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

func TestOrderRemainingQty(t *testing.T) {
	o := &Order{Quantity: 100, FilledQty: 40}
	if o.RemainingQty() != 60 {
		t.Errorf("RemainingQty() = %d, want 60", o.RemainingQty())
	}
}

func TestOrderIsFilled(t *testing.T) {
	o := &Order{Quantity: 100, FilledQty: 100}
	if !o.IsFilled() {
		t.Error("expected IsFilled() = true")
	}
	o.FilledQty = 99
	if o.IsFilled() {
		t.Error("expected IsFilled() = false")
	}
}

func TestOrderString(t *testing.T) {
	o := &Order{
		ID: 42, ClientID: "c1", Side: SideBuy, Type: OrderTypeLimit,
		Quantity: 100, FilledQty: 50, Price: 5000,
	}
	s := o.String()
	if !strings.Contains(s, "42") || !strings.Contains(s, "BUY") {
		t.Errorf("unexpected String(): %s", s)
	}
}

func TestNextOrderIDMonotonic(t *testing.T) {
	a := NextOrderID()
	b := NextOrderID()
	if b <= a {
		t.Errorf("IDs should be monotonically increasing: %d, %d", a, b)
	}
}

// ── SetMinOrderID / SetMinTradeID ────────────────────────────────────────────

func TestSetMinOrderID(t *testing.T) {
	// Set to a high value
	SetMinOrderID(999_999)
	next := NextOrderID()
	if next <= 999_999 {
		t.Errorf("next ID after SetMinOrderID(999999) = %d, expected > 999999", next)
	}

	// Setting to a lower value should be a no-op
	before := NextOrderID()
	SetMinOrderID(1)
	after := NextOrderID()
	if after <= before {
		t.Error("SetMinOrderID with lower value should not decrease the counter")
	}
}

func TestSetMinTradeID(t *testing.T) {
	SetMinTradeID(888_888)
	next := NextTradeID()
	if next <= 888_888 {
		t.Errorf("next ID after SetMinTradeID(888888) = %d, expected > 888888", next)
	}
}

func TestSetMinOrderIDConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			SetMinOrderID(id)
		}(uint64(2_000_000 + i))
	}
	wg.Wait()
	next := NextOrderID()
	if next <= 2_000_099 {
		t.Errorf("after concurrent SetMinOrderID, next = %d, expected > 2000099", next)
	}
}

// ── Trade ────────────────────────────────────────────────────────────────────

func TestNewTrade(t *testing.T) {
	buy := &Order{ID: 1, ClientID: "buyer"}
	sell := &Order{ID: 2, ClientID: "seller"}
	trade := NewTrade("BTC-USD", buy, sell, 50000, 100, SideBuy, 0)

	if trade.ID == 0 {
		t.Error("trade ID should be non-zero")
	}
	if trade.Instrument != "BTC-USD" {
		t.Errorf("Instrument = %s", trade.Instrument)
	}
	if trade.BuyOrderID != 1 {
		t.Errorf("BuyOrderID = %d", trade.BuyOrderID)
	}
	if trade.SellOrderID != 2 {
		t.Errorf("SellOrderID = %d", trade.SellOrderID)
	}
	if trade.BuyClientID != "buyer" {
		t.Errorf("BuyClientID = %s", trade.BuyClientID)
	}
	if trade.SellClientID != "seller" {
		t.Errorf("SellClientID = %s", trade.SellClientID)
	}
	if trade.Price != 50000 {
		t.Errorf("Price = %d", trade.Price)
	}
	if trade.Quantity != 100 {
		t.Errorf("Quantity = %d", trade.Quantity)
	}
	if trade.Aggressor != SideBuy {
		t.Errorf("Aggressor = %d", trade.Aggressor)
	}
	if trade.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
	if trade.SequenceNo != trade.ID {
		t.Errorf("SequenceNo = %d with walSeq=0 should match trade ID %d", trade.SequenceNo, trade.ID)
	}
}

// ── Price scaling ────────────────────────────────────────────────────────────

func TestFloatToPrice(t *testing.T) {
	p := FloatToPrice(50000.12345678)
	f := PriceToFloat(p)
	diff := f - 50000.12345678
	if diff < -0.00000001 || diff > 0.00000001 {
		t.Errorf("round-trip: %f != 50000.12345678", f)
	}
}

func TestFloatToQty(t *testing.T) {
	q := FloatToQty(1.5)
	f := QtyToFloat(q)
	if f != 1.5 {
		t.Errorf("round-trip: %f != 1.5", f)
	}
}

func TestPriceScaleZero(t *testing.T) {
	if FloatToPrice(0) != 0 {
		t.Error("FloatToPrice(0) should be 0")
	}
	if PriceToFloat(0) != 0 {
		t.Error("PriceToFloat(0) should be 0")
	}
}

func TestQtyScaleZero(t *testing.T) {
	if FloatToQty(0) != 0 {
		t.Error("FloatToQty(0) should be 0")
	}
	if QtyToFloat(0) != 0 {
		t.Error("QtyToFloat(0) should be 0")
	}
}
