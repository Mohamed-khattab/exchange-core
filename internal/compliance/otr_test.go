package compliance

import (
	"testing"
)

func TestOTRRecordAndCheck(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)

	// Record 20 orders, 1 trade → ratio 20:1, above threshold 10
	for i := 0; i < 20; i++ {
		tracker.RecordOrder("client-a", "BTC-USD")
	}
	tracker.RecordTrade("client-a", "BTC-USD")

	ratio := tracker.CheckRatio("client-a", "BTC-USD")
	if ratio < 10.0 {
		t.Errorf("ratio = %f, expected > 10.0", ratio)
	}
}

func TestOTRBelowThreshold(t *testing.T) {
	tracker := NewOTRTracker(60, 100.0, OTRAlert)

	tracker.RecordOrder("client-a", "BTC-USD")
	tracker.RecordTrade("client-a", "BTC-USD")

	ratio := tracker.CheckRatio("client-a", "BTC-USD")
	if ratio > 100.0 {
		t.Errorf("ratio = %f, should be below threshold", ratio)
	}
}

func TestOTREmptyClient(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)
	ratio := tracker.CheckRatio("", "BTC-USD")
	if ratio != 0 {
		t.Errorf("empty client ratio = %f", ratio)
	}
}

func TestOTRUnknownClient(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)
	ratio := tracker.CheckRatio("unknown", "BTC-USD")
	if ratio != 0 {
		t.Errorf("unknown client ratio = %f", ratio)
	}
}

func TestOTRRejectAction(t *testing.T) {
	tracker := NewOTRTracker(60, 5.0, OTRReject)

	// 10 orders, 0 trades → ratio = 10, above threshold 5
	for i := 0; i < 10; i++ {
		tracker.RecordOrder("client-a", "BTC-USD")
	}

	rejected := tracker.CheckAndRecord("client-a", "BTC-USD")
	if !rejected {
		t.Error("expected rejection when OTR > threshold and action is REJECT")
	}
}

func TestOTRAlertAction(t *testing.T) {
	var alertFired bool
	tracker := NewOTRTracker(60, 5.0, OTRAlert)
	tracker.SetAlertCallback(func(clientID, instrument string, ratio float64) {
		alertFired = true
	})

	for i := 0; i < 10; i++ {
		tracker.RecordOrder("client-a", "BTC-USD")
	}

	rejected := tracker.CheckAndRecord("client-a", "BTC-USD")
	if rejected {
		t.Error("should not reject in ALERT mode")
	}
	if !alertFired {
		t.Error("alert callback should have fired")
	}
}

func TestOTRPerClientIsolation(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)

	for i := 0; i < 20; i++ {
		tracker.RecordOrder("client-a", "BTC-USD")
	}

	// client-b should have ratio 0
	ratio := tracker.CheckRatio("client-b", "BTC-USD")
	if ratio != 0 {
		t.Errorf("client-b ratio = %f, expected 0", ratio)
	}
}

func TestOTRPerInstrumentIsolation(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)

	for i := 0; i < 20; i++ {
		tracker.RecordOrder("client-a", "BTC-USD")
	}

	ratio := tracker.CheckRatio("client-a", "ETH-USD")
	if ratio != 0 {
		t.Errorf("wrong instrument ratio = %f, expected 0", ratio)
	}
}

func TestOTRNoTradesDenominator(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)

	for i := 0; i < 5; i++ {
		tracker.RecordOrder("client-a", "BTC-USD")
	}

	// With 0 trades, ratio should be the order count
	ratio := tracker.CheckRatio("client-a", "BTC-USD")
	if ratio != 5.0 {
		t.Errorf("ratio = %f, expected 5.0 (orders with 0 trades)", ratio)
	}
}

func TestOTRCleanup(t *testing.T) {
	tracker := NewOTRTracker(1, 10.0, OTRAlert) // 1-second window

	tracker.RecordOrder("client-a", "BTC-USD")

	// Force lastTS to be old
	tracker.mu.Lock()
	for _, c := range tracker.counters {
		c.lastTS -= 10
	}
	tracker.mu.Unlock()

	tracker.Cleanup()

	tracker.mu.Lock()
	count := len(tracker.counters)
	tracker.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", count)
	}
}

func TestParseOTRAction(t *testing.T) {
	if ParseOTRAction("REJECT") != OTRReject {
		t.Error("REJECT")
	}
	if ParseOTRAction("ALERT") != OTRAlert {
		t.Error("ALERT")
	}
	if ParseOTRAction("unknown") != OTRAlert {
		t.Error("default should be ALERT")
	}
}

func TestOTRRecordCancel(t *testing.T) {
	tracker := NewOTRTracker(60, 10.0, OTRAlert)
	tracker.RecordCancel("client-a", "BTC-USD")
	// Should not panic, just increment cancel counter
}
