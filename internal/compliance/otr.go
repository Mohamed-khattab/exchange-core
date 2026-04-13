// Package compliance implements regulatory compliance monitoring.
package compliance

import (
	"sync"
	"time"
)

// OTRAction defines the action taken when OTR threshold is exceeded.
type OTRAction int

const (
	OTRAlert  OTRAction = iota // Log alert but allow order
	OTRReject                  // Reject the order
)

// ParseOTRAction converts a string to OTRAction.
func ParseOTRAction(s string) OTRAction {
	switch s {
	case "REJECT":
		return OTRReject
	default:
		return OTRAlert
	}
}

type clientInstrumentKey struct {
	ClientID   string
	Instrument string
}

type otrCounters struct {
	orders  [60]uint32 // ring buffer, one slot per second
	trades  [60]uint32
	cancels [60]uint32
	head    int
	lastTS  int64 // unix second of head slot
}

// OTRTracker monitors order-to-trade ratios per client per instrument.
type OTRTracker struct {
	mu        sync.Mutex
	counters  map[clientInstrumentKey]*otrCounters
	window    int     // seconds
	threshold float64 // e.g., 100.0 = 100:1 ratio
	action    OTRAction
	onAlert   func(clientID, instrument string, ratio float64)
}

// NewOTRTracker creates a new OTR tracker.
func NewOTRTracker(windowSec int, threshold float64, action OTRAction) *OTRTracker {
	if windowSec <= 0 || windowSec > 60 {
		windowSec = 60
	}
	return &OTRTracker{
		counters:  make(map[clientInstrumentKey]*otrCounters),
		window:    windowSec,
		threshold: threshold,
		action:    action,
	}
}

// SetAlertCallback sets a function called when OTR threshold is exceeded.
func (t *OTRTracker) SetAlertCallback(fn func(clientID, instrument string, ratio float64)) {
	t.onAlert = fn
}

// Threshold returns the configured OTR threshold.
func (t *OTRTracker) Threshold() float64 {
	return t.threshold
}

// Action returns the configured OTR action.
func (t *OTRTracker) Action() OTRAction {
	return t.action
}

// RecordOrder increments the order count for a client.
func (t *OTRTracker) RecordOrder(clientID, instrument string) {
	if clientID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.getOrCreate(clientID, instrument)
	t.advance(c)
	c.orders[c.head]++
}

// RecordTrade increments the trade count for a client.
func (t *OTRTracker) RecordTrade(clientID, instrument string) {
	if clientID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.getOrCreate(clientID, instrument)
	t.advance(c)
	c.trades[c.head]++
}

// RecordCancel increments the cancellation count for a client.
func (t *OTRTracker) RecordCancel(clientID, instrument string) {
	if clientID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.getOrCreate(clientID, instrument)
	t.advance(c)
	c.cancels[c.head]++
}

// CheckRatio computes the order-to-trade ratio for a client over the window.
// Returns the ratio (orders / max(trades, 1)).
func (t *OTRTracker) CheckRatio(clientID, instrument string) float64 {
	if clientID == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := clientInstrumentKey{clientID, instrument}
	c, ok := t.counters[key]
	if !ok {
		return 0
	}
	t.advance(c)

	var totalOrders, totalTrades uint32
	for i := 0; i < t.window; i++ {
		idx := (c.head - i + 60) % 60
		totalOrders += c.orders[idx]
		totalTrades += c.trades[idx]
	}

	if totalTrades == 0 {
		if totalOrders == 0 {
			return 0
		}
		return float64(totalOrders) // infinite ratio, capped to order count
	}
	return float64(totalOrders) / float64(totalTrades)
}

// CheckAndRecord records an order and checks if the OTR threshold is exceeded.
// Returns true if the order should be rejected (only when action is OTRReject).
func (t *OTRTracker) CheckAndRecord(clientID, instrument string) bool {
	if clientID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.getOrCreate(clientID, instrument)
	t.advance(c)
	c.orders[c.head]++

	var totalOrders, totalTrades uint32
	for i := 0; i < t.window; i++ {
		idx := (c.head - i + 60) % 60
		totalOrders += c.orders[idx]
		totalTrades += c.trades[idx]
	}

	var ratio float64
	if totalTrades == 0 {
		if totalOrders == 0 {
			ratio = 0
		} else {
			ratio = float64(totalOrders)
		}
	} else {
		ratio = float64(totalOrders) / float64(totalTrades)
	}

	if ratio > t.threshold {
		if t.onAlert != nil {
			t.onAlert(clientID, instrument, ratio)
		}
		return t.action == OTRReject
	}
	return false
}

// Cleanup removes entries that haven't been active for 2x the window duration.
func (t *OTRTracker) Cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().Unix()
	cutoff := int64(t.window * 2)
	for key, c := range t.counters {
		if now-c.lastTS > cutoff {
			delete(t.counters, key)
		}
	}
}

func (t *OTRTracker) getOrCreate(clientID, instrument string) *otrCounters {
	key := clientInstrumentKey{clientID, instrument}
	c, ok := t.counters[key]
	if !ok {
		c = &otrCounters{lastTS: time.Now().Unix()}
		t.counters[key] = c
	}
	return c
}

// advance moves the ring buffer head forward to the current second,
// zeroing any skipped slots.
func (t *OTRTracker) advance(c *otrCounters) {
	now := time.Now().Unix()
	elapsed := now - c.lastTS
	if elapsed <= 0 {
		return
	}
	if elapsed > 60 {
		elapsed = 60
	}
	for i := int64(0); i < elapsed; i++ {
		c.head = (c.head + 1) % 60
		c.orders[c.head] = 0
		c.trades[c.head] = 0
		c.cancels[c.head] = 0
	}
	c.lastTS = now
}
