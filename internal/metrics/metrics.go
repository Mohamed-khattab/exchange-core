// Package metrics provides a lightweight in-memory metrics collector.
// In production, replace with Prometheus client_golang.
package metrics

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// ── LatencyHistogram ──────────────────────────────────────────────────────────

type LatencyHistogram struct {
	mu      sync.Mutex
	buckets [10]uint64 // <1µs, <10µs, <100µs, <1ms, <10ms, <100ms, <1s, ≥1s
	total   uint64
	sumNs   uint64
}

var boundaries = []time.Duration{
	1 * time.Microsecond,
	10 * time.Microsecond,
	100 * time.Microsecond,
	1 * time.Millisecond,
	10 * time.Millisecond,
	100 * time.Millisecond,
	1 * time.Second,
}

func (h *LatencyHistogram) Observe(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	h.sumNs += uint64(d.Nanoseconds())
	for i, b := range boundaries {
		if d < b {
			h.buckets[i]++
			return
		}
	}
	h.buckets[len(boundaries)]++
}

func (h *LatencyHistogram) AvgNs() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total == 0 {
		return 0
	}
	return h.sumNs / h.total
}

func (h *LatencyHistogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

// ── InstrumentMetrics ─────────────────────────────────────────────────────────

type InstrumentMetrics struct {
	OrdersProcessed uint64
	TradesExecuted  uint64
	VolumeTraded    uint64 // in qty units
	Cancellations   uint64
	BackPressure    uint64
	Latency         LatencyHistogram
}

// ── Collector ─────────────────────────────────────────────────────────────────

type Collector struct {
	mu          sync.RWMutex
	instruments map[string]*InstrumentMetrics
	startTime   time.Time
}

func NewCollector() *Collector {
	return &Collector{
		instruments: make(map[string]*InstrumentMetrics),
		startTime:   time.Now(),
	}
}

func (c *Collector) getOrCreate(instrument string) *InstrumentMetrics {
	c.mu.RLock()
	m, ok := c.instruments[instrument]
	c.mu.RUnlock()
	if ok {
		return m
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok = c.instruments[instrument]; !ok {
		m = &InstrumentMetrics{}
		c.instruments[instrument] = m
	}
	return m
}

func (c *Collector) RecordOrderProcessed(instrument string, latency time.Duration) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.OrdersProcessed, 1)
	m.Latency.Observe(latency)
}

func (c *Collector) RecordTrade(instrument string, trade *models.Trade) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.TradesExecuted, 1)
	atomic.AddUint64(&m.VolumeTraded, trade.Quantity)
}

func (c *Collector) RecordCancellation(instrument string) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.Cancellations, 1)
}

func (c *Collector) RecordBackPressure(instrument string) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.BackPressure, 1)
}

// Snapshot returns a point-in-time copy of all metrics.
func (c *Collector) Snapshot() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := map[string]interface{}{
		"uptime_seconds": time.Since(c.startTime).Seconds(),
	}

	instruments := make(map[string]interface{})
	for inst, m := range c.instruments {
		instruments[inst] = map[string]interface{}{
			"orders_processed": atomic.LoadUint64(&m.OrdersProcessed),
			"trades_executed":  atomic.LoadUint64(&m.TradesExecuted),
			"volume_traded":    models.QtyToFloat(atomic.LoadUint64(&m.VolumeTraded)),
			"cancellations":    atomic.LoadUint64(&m.Cancellations),
			"back_pressure":    atomic.LoadUint64(&m.BackPressure),
			"avg_latency_ns":   m.Latency.AvgNs(),
			"order_count":      m.Latency.Count(),
		}
	}
	result["instruments"] = instruments
	return result
}
