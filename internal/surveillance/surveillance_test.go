package surveillance

import (
	"testing"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// ── Spoofing ─────────────────────────────────────────────────────────────────

func TestSpoofingDetected(t *testing.T) {
	d := NewSpoofingDetector(true, 500, 100) // 500ms window, min qty 100

	order := &models.Order{ID: 1, Quantity: 200, Price: 50000, ClientID: "spoofer"}
	now := time.Now()

	// Place order
	alerts := d.OnEvent(&Event{
		Type: EventOrderPlaced, Timestamp: now,
		Order: order, Instrument: "BTC-USD", ClientID: "spoofer",
	})
	if len(alerts) != 0 {
		t.Error("no alert on placement")
	}

	// Cancel within window
	alerts = d.OnEvent(&Event{
		Type: EventOrderCancelled, Timestamp: now.Add(200 * time.Millisecond),
		Order: order, Instrument: "BTC-USD", ClientID: "spoofer",
	})
	if len(alerts) != 1 {
		t.Fatalf("expected 1 spoofing alert, got %d", len(alerts))
	}
	if alerts[0].Severity != "WARNING" {
		t.Errorf("severity = %s", alerts[0].Severity)
	}
}

func TestSpoofingBelowQtyThreshold(t *testing.T) {
	d := NewSpoofingDetector(true, 500, 1000) // min qty 1000

	order := &models.Order{ID: 1, Quantity: 50, ClientID: "small"}

	d.OnEvent(&Event{Type: EventOrderPlaced, Timestamp: time.Now(), Order: order, ClientID: "small"})
	alerts := d.OnEvent(&Event{Type: EventOrderCancelled, Timestamp: time.Now(), Order: order, ClientID: "small"})
	if len(alerts) != 0 {
		t.Error("should not alert for small orders")
	}
}

func TestSpoofingOutsideWindow(t *testing.T) {
	d := NewSpoofingDetector(true, 100, 1) // 100ms window

	order := &models.Order{ID: 1, Quantity: 100, ClientID: "slow"}
	now := time.Now()

	d.OnEvent(&Event{Type: EventOrderPlaced, Timestamp: now, Order: order, ClientID: "slow"})
	alerts := d.OnEvent(&Event{
		Type: EventOrderCancelled, Timestamp: now.Add(200 * time.Millisecond),
		Order: order, ClientID: "slow",
	})
	if len(alerts) != 0 {
		t.Error("should not alert when cancel is outside window")
	}
}

func TestSpoofingDisabled(t *testing.T) {
	d := NewSpoofingDetector(false, 500, 1)
	if d.Enabled() {
		t.Error("should be disabled")
	}
}

// ── Layering ─────────────────────────────────────────────────────────────────

func TestLayeringDetected(t *testing.T) {
	d := NewLayeringDetector(true, 3, 5000) // 3 levels, 5 second window
	now := time.Now()

	// Place 3 orders at different prices on same side
	for i := 0; i < 3; i++ {
		order := &models.Order{
			ID: uint64(i), Price: int64(50000 + i*100),
			Side: models.SideBuy, ClientID: "layerer",
		}
		alerts := d.OnEvent(&Event{
			Type: EventOrderPlaced, Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Order: order, Instrument: "BTC-USD", ClientID: "layerer",
		})
		if i < 2 && len(alerts) != 0 {
			t.Errorf("no alert until 3rd level, got alert at %d", i)
		}
		if i == 2 && len(alerts) != 1 {
			t.Fatalf("expected layering alert at 3rd level, got %d", len(alerts))
		}
	}
}

func TestLayeringDifferentSides(t *testing.T) {
	d := NewLayeringDetector(true, 3, 5000)
	now := time.Now()

	// 2 buys + 1 sell = only 2 on buy side, below threshold
	for i := 0; i < 2; i++ {
		d.OnEvent(&Event{
			Type: EventOrderPlaced, Timestamp: now,
			Order: &models.Order{ID: uint64(i), Price: int64(50000 + i*100), Side: models.SideBuy, ClientID: "c"},
			Instrument: "BTC-USD", ClientID: "c",
		})
	}
	alerts := d.OnEvent(&Event{
		Type: EventOrderPlaced, Timestamp: now,
		Order: &models.Order{ID: 10, Price: 49000, Side: models.SideSell, ClientID: "c"},
		Instrument: "BTC-USD", ClientID: "c",
	})
	if len(alerts) != 0 {
		t.Error("should not alert across different sides")
	}
}

// ── Wash Trading ─────────────────────────────────────────────────────────────

func TestWashTradingSTPTriggered(t *testing.T) {
	d := NewWashTradingDetector(true)

	alerts := d.OnEvent(&Event{
		Type:       EventSTPTriggered,
		Instrument: "BTC-USD",
		ClientID:   "washer",
		Timestamp:  time.Now(),
	})
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != "INFO" {
		t.Errorf("severity = %s, want INFO", alerts[0].Severity)
	}
}

func TestWashTradingSelfTrade(t *testing.T) {
	d := NewWashTradingDetector(true)

	trade := &models.Trade{
		ID: 1, BuyClientID: "washer", SellClientID: "washer",
		BuyOrderID: 100, SellOrderID: 200,
	}
	alerts := d.OnEvent(&Event{
		Type:       EventTradeExecuted,
		Trade:      trade,
		Instrument: "BTC-USD",
		ClientID:   "washer",
		Timestamp:  time.Now(),
	})
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != "CRITICAL" {
		t.Errorf("severity = %s, want CRITICAL", alerts[0].Severity)
	}
}

func TestWashTradingNormalTrade(t *testing.T) {
	d := NewWashTradingDetector(true)

	trade := &models.Trade{
		ID: 1, BuyClientID: "buyer", SellClientID: "seller",
	}
	alerts := d.OnEvent(&Event{
		Type: EventTradeExecuted, Trade: trade,
		Instrument: "BTC-USD", Timestamp: time.Now(),
	})
	if len(alerts) != 0 {
		t.Error("should not alert for normal trade")
	}
}

// ── Monitor ──────────────────────────────────────────────────────────────────

func TestMonitorDispatch(t *testing.T) {
	d := NewSpoofingDetector(true, 1000, 1)
	m := NewMonitor([]Detector{d}, 100)
	go m.Run()
	defer m.Stop()

	order := &models.Order{ID: 1, Quantity: 100, ClientID: "test"}

	m.eventCh <- &Event{Type: EventOrderPlaced, Timestamp: time.Now(), Order: order, ClientID: "test", Instrument: "BTC-USD"}
	m.eventCh <- &Event{Type: EventOrderCancelled, Timestamp: time.Now(), Order: order, ClientID: "test", Instrument: "BTC-USD"}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	alerts := m.RecentAlerts("", time.Time{})
	if len(alerts) < 1 {
		t.Errorf("expected at least 1 alert, got %d", len(alerts))
	}
}

func TestMonitorRecentAlertsFilter(t *testing.T) {
	m := NewMonitor(nil, 100)
	m.recordAlert(Alert{DetectorName: "test", Instrument: "BTC-USD", Timestamp: time.Now()})
	m.recordAlert(Alert{DetectorName: "test", Instrument: "ETH-USD", Timestamp: time.Now()})

	btc := m.RecentAlerts("BTC-USD", time.Time{})
	if len(btc) != 1 {
		t.Errorf("BTC alerts = %d, want 1", len(btc))
	}

	all := m.RecentAlerts("", time.Time{})
	if len(all) != 2 {
		t.Errorf("all alerts = %d, want 2", len(all))
	}
}
