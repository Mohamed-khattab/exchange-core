// Package metrics provides a metrics collector with dual-write:
// in-memory atomics for the JSON /v1/stats API, and Prometheus for /metrics scraping.
package metrics

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/trading/matching-engine/internal/models"
)

// ── LatencyHistogram (in-memory, backward compat) ────────────────────────────

type LatencyHistogram struct {
	mu      sync.Mutex
	buckets [10]uint64
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

// ── InstrumentMetrics (in-memory counters) ───────────────────────────────────

type InstrumentMetrics struct {
	OrdersProcessed uint64
	TradesExecuted  uint64
	VolumeTraded    uint64
	Cancellations   uint64
	BackPressure    uint64
	Latency         LatencyHistogram
}

// ── Collector ────────────────────────────────────────────────────────────────

type Collector struct {
	mu          sync.RWMutex
	instruments map[string]*InstrumentMetrics
	startTime   time.Time

	// Prometheus registry (custom per collector for test isolation)
	Registry *prometheus.Registry

	// Prometheus metrics
	promOrdersProcessed     *prometheus.CounterVec
	promTradesExecuted      *prometheus.CounterVec
	promCancellations       *prometheus.CounterVec
	promBackPressure        *prometheus.CounterVec
	promVolumeTraded        *prometheus.GaugeVec
	promOrderLatency        *prometheus.HistogramVec
	promOpenOrders          *prometheus.GaugeVec
	promBidLevels           *prometheus.GaugeVec
	promAskLevels           *prometheus.GaugeVec
	promSTPCancellations    *prometheus.CounterVec
	promCircuitBreakerTrips *prometheus.CounterVec
	promWSClients           prometheus.Gauge
}

func NewCollector() *Collector {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	c := &Collector{
		instruments: make(map[string]*InstrumentMetrics),
		startTime:   time.Now(),
		Registry:    reg,
	}

	c.promOrdersProcessed = factory.NewCounterVec(prometheus.CounterOpts{
		Namespace: "matching_engine",
		Name:      "orders_processed_total",
		Help:      "Total number of orders processed",
	}, []string{"instrument"})

	c.promTradesExecuted = factory.NewCounterVec(prometheus.CounterOpts{
		Namespace: "matching_engine",
		Name:      "trades_executed_total",
		Help:      "Total number of trades executed",
	}, []string{"instrument"})

	c.promCancellations = factory.NewCounterVec(prometheus.CounterOpts{
		Namespace: "matching_engine",
		Name:      "cancellations_total",
		Help:      "Total number of order cancellations",
	}, []string{"instrument"})

	c.promBackPressure = factory.NewCounterVec(prometheus.CounterOpts{
		Namespace: "matching_engine",
		Name:      "back_pressure_total",
		Help:      "Total number of back-pressure events (queue full)",
	}, []string{"instrument"})

	c.promVolumeTraded = factory.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "matching_engine",
		Name:      "volume_traded",
		Help:      "Total volume traded in base asset units",
	}, []string{"instrument"})

	c.promOrderLatency = factory.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "matching_engine",
		Name:      "order_latency_seconds",
		Help:      "Order processing latency in seconds",
		Buckets:   []float64{1e-6, 10e-6, 100e-6, 1e-3, 10e-3, 100e-3, 1.0},
	}, []string{"instrument"})

	c.promOpenOrders = factory.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "matching_engine",
		Name:      "open_orders",
		Help:      "Current number of open orders",
	}, []string{"instrument"})

	c.promBidLevels = factory.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "matching_engine",
		Name:      "bid_levels",
		Help:      "Current number of bid price levels",
	}, []string{"instrument"})

	c.promAskLevels = factory.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "matching_engine",
		Name:      "ask_levels",
		Help:      "Current number of ask price levels",
	}, []string{"instrument"})

	c.promSTPCancellations = factory.NewCounterVec(prometheus.CounterOpts{
		Namespace: "matching_engine",
		Name:      "stp_cancellations_total",
		Help:      "Total self-trade prevention cancellations",
	}, []string{"instrument", "mode"})

	c.promCircuitBreakerTrips = factory.NewCounterVec(prometheus.CounterOpts{
		Namespace: "matching_engine",
		Name:      "circuit_breaker_trips_total",
		Help:      "Total circuit breaker trip events",
	}, []string{"instrument"})

	c.promWSClients = factory.NewGauge(prometheus.GaugeOpts{
		Namespace: "matching_engine",
		Name:      "ws_connected_clients",
		Help:      "Current number of connected WebSocket clients",
	})

	return c
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

	c.promOrdersProcessed.WithLabelValues(instrument).Inc()
	c.promOrderLatency.WithLabelValues(instrument).Observe(latency.Seconds())
}

func (c *Collector) RecordTrade(instrument string, trade *models.Trade) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.TradesExecuted, 1)
	atomic.AddUint64(&m.VolumeTraded, trade.Quantity)

	c.promTradesExecuted.WithLabelValues(instrument).Inc()
	c.promVolumeTraded.WithLabelValues(instrument).Add(models.QtyToFloat(trade.Quantity))
}

func (c *Collector) RecordCancellation(instrument string) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.Cancellations, 1)

	c.promCancellations.WithLabelValues(instrument).Inc()
}

func (c *Collector) RecordBackPressure(instrument string) {
	m := c.getOrCreate(instrument)
	atomic.AddUint64(&m.BackPressure, 1)

	c.promBackPressure.WithLabelValues(instrument).Inc()
}

func (c *Collector) RecordSTPCancellation(instrument string, mode models.STPMode) {
	c.promSTPCancellations.WithLabelValues(instrument, string(mode)).Inc()
}

func (c *Collector) RecordCircuitBreakerTrip(instrument string) {
	c.promCircuitBreakerTrips.WithLabelValues(instrument).Inc()
}

func (c *Collector) SetOpenOrders(instrument string, count int) {
	c.promOpenOrders.WithLabelValues(instrument).Set(float64(count))
}

func (c *Collector) SetBidLevels(instrument string, count int) {
	c.promBidLevels.WithLabelValues(instrument).Set(float64(count))
}

func (c *Collector) SetAskLevels(instrument string, count int) {
	c.promAskLevels.WithLabelValues(instrument).Set(float64(count))
}

func (c *Collector) SetWSConnectedClients(count int) {
	c.promWSClients.Set(float64(count))
}

// Snapshot returns a point-in-time copy of all in-memory metrics (backward compatible).
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
