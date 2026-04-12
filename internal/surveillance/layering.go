package surveillance

import (
	"fmt"
	"sync"
	"time"
)

// LayeringDetector detects potential layering: orders at many price levels on the same side.
type LayeringDetector struct {
	enabled          bool
	priceLevels      int
	timeWindowMs     int64
	mu               sync.Mutex
	recentOrders     map[string][]layeringRecord // clientID -> recent orders
}

type layeringRecord struct {
	Price    int64
	Side     string
	PlacedAt time.Time
}

// NewLayeringDetector creates a layering detector.
func NewLayeringDetector(enabled bool, priceLevels int, timeWindowMs int64) *LayeringDetector {
	return &LayeringDetector{
		enabled:      enabled,
		priceLevels:  priceLevels,
		timeWindowMs: timeWindowMs,
		recentOrders: make(map[string][]layeringRecord),
	}
}

func (d *LayeringDetector) Name() string  { return "layering" }
func (d *LayeringDetector) Enabled() bool { return d.enabled }

func (d *LayeringDetector) OnEvent(event *Event) []Alert {
	if event.Type != EventOrderPlaced || event.Order == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	side := event.Order.Side.String()
	d.recentOrders[event.ClientID] = append(d.recentOrders[event.ClientID], layeringRecord{
		Price:    event.Order.Price,
		Side:     side,
		PlacedAt: event.Timestamp,
	})
	d.gc(event.ClientID)

	// Count distinct prices on the same side within the time window
	cutoff := event.Timestamp.Add(-time.Duration(d.timeWindowMs) * time.Millisecond)
	prices := make(map[int64]bool)
	for _, rec := range d.recentOrders[event.ClientID] {
		if rec.Side == side && rec.PlacedAt.After(cutoff) {
			prices[rec.Price] = true
		}
	}

	if len(prices) >= d.priceLevels {
		return []Alert{{
			DetectorName: d.Name(),
			Severity:     "WARNING",
			Instrument:   event.Instrument,
			ClientID:     event.ClientID,
			Description:  fmt.Sprintf("potential layering: %d price levels on %s side within %dms", len(prices), side, d.timeWindowMs),
			Timestamp:    event.Timestamp,
			Evidence: map[string]interface{}{
				"price_levels":     len(prices),
				"side":             side,
				"time_window_ms":   d.timeWindowMs,
				"threshold_levels": d.priceLevels,
			},
		}}
	}

	return nil
}

func (d *LayeringDetector) gc(clientID string) {
	records := d.recentOrders[clientID]
	cutoff := time.Now().Add(-time.Duration(d.timeWindowMs*2) * time.Millisecond)
	kept := records[:0]
	for _, r := range records {
		if r.PlacedAt.After(cutoff) {
			kept = append(kept, r)
		}
	}
	d.recentOrders[clientID] = kept
}
