package surveillance

import (
	"fmt"
	"sync"
	"time"
)

type orderRecord struct {
	OrderID   uint64
	Quantity  uint64
	Price     int64
	PlacedAt  time.Time
}

// SpoofingDetector detects potential spoofing: large orders placed then quickly cancelled.
type SpoofingDetector struct {
	enabled        bool
	cancelWindowMs int64
	minQtyThreshold uint64
	mu             sync.Mutex
	recentOrders   map[string][]orderRecord // clientID -> recent orders
}

// NewSpoofingDetector creates a spoofing detector.
func NewSpoofingDetector(enabled bool, cancelWindowMs int64, minQtyThreshold uint64) *SpoofingDetector {
	return &SpoofingDetector{
		enabled:         enabled,
		cancelWindowMs:  cancelWindowMs,
		minQtyThreshold: minQtyThreshold,
		recentOrders:    make(map[string][]orderRecord),
	}
}

func (d *SpoofingDetector) Name() string    { return "spoofing" }
func (d *SpoofingDetector) Enabled() bool   { return d.enabled }

func (d *SpoofingDetector) OnEvent(event *Event) []Alert {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch event.Type {
	case EventOrderPlaced:
		if event.Order == nil {
			return nil
		}
		d.recentOrders[event.ClientID] = append(d.recentOrders[event.ClientID], orderRecord{
			OrderID:  event.Order.ID,
			Quantity: event.Order.Quantity,
			Price:    event.Order.Price,
			PlacedAt: event.Timestamp,
		})
		d.gc(event.ClientID)
		return nil

	case EventOrderCancelled:
		if event.Order == nil {
			return nil
		}
		records := d.recentOrders[event.ClientID]
		now := event.Timestamp
		for i, rec := range records {
			if rec.OrderID == event.Order.ID {
				elapsed := now.Sub(rec.PlacedAt).Milliseconds()
				if elapsed <= d.cancelWindowMs && rec.Quantity >= d.minQtyThreshold {
					// Remove the matched record
					d.recentOrders[event.ClientID] = append(records[:i], records[i+1:]...)
					return []Alert{{
						DetectorName: d.Name(),
						Severity:     "WARNING",
						Instrument:   event.Instrument,
						ClientID:     event.ClientID,
						Description:  fmt.Sprintf("potential spoofing: order %d (qty=%d) cancelled after %dms", rec.OrderID, rec.Quantity, elapsed),
						Timestamp:    now,
						Evidence: map[string]interface{}{
							"order_id":     rec.OrderID,
							"quantity":     rec.Quantity,
							"elapsed_ms":   elapsed,
							"cancel_window_ms": d.cancelWindowMs,
						},
					}}
				}
				break
			}
		}
		return nil
	}
	return nil
}

func (d *SpoofingDetector) gc(clientID string) {
	records := d.recentOrders[clientID]
	cutoff := time.Now().Add(-time.Duration(d.cancelWindowMs*2) * time.Millisecond)
	kept := records[:0]
	for _, r := range records {
		if r.PlacedAt.After(cutoff) {
			kept = append(kept, r)
		}
	}
	d.recentOrders[clientID] = kept
}
