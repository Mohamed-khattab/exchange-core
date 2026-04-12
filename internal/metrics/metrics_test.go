package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

func TestLatencyHistogramObserve(t *testing.T) {
	h := &LatencyHistogram{}

	h.Observe(500 * time.Nanosecond)   // bucket 0 (<1µs)
	h.Observe(5 * time.Microsecond)     // bucket 1 (<10µs)
	h.Observe(50 * time.Microsecond)    // bucket 2 (<100µs)
	h.Observe(500 * time.Microsecond)   // bucket 3 (<1ms)
	h.Observe(5 * time.Millisecond)     // bucket 4 (<10ms)
	h.Observe(50 * time.Millisecond)    // bucket 5 (<100ms)
	h.Observe(500 * time.Millisecond)   // bucket 6 (<1s)
	h.Observe(2 * time.Second)          // bucket 7 (≥1s)

	if h.Count() != 8 {
		t.Errorf("Count() = %d, want 8", h.Count())
	}
	if h.AvgNs() == 0 {
		t.Error("AvgNs() should be non-zero")
	}
}

func TestLatencyHistogramEmpty(t *testing.T) {
	h := &LatencyHistogram{}
	if h.AvgNs() != 0 {
		t.Errorf("AvgNs() on empty histogram = %d, want 0", h.AvgNs())
	}
	if h.Count() != 0 {
		t.Errorf("Count() on empty histogram = %d, want 0", h.Count())
	}
}

func TestCollectorRecordOrderProcessed(t *testing.T) {
	c := NewCollector()
	c.RecordOrderProcessed("BTC-USD", 10*time.Microsecond)
	c.RecordOrderProcessed("BTC-USD", 20*time.Microsecond)

	snap := c.Snapshot()
	instruments := snap["instruments"].(map[string]interface{})
	btc := instruments["BTC-USD"].(map[string]interface{})

	if btc["orders_processed"].(uint64) != 2 {
		t.Errorf("orders_processed = %v, want 2", btc["orders_processed"])
	}
	if btc["order_count"].(uint64) != 2 {
		t.Errorf("order_count = %v, want 2", btc["order_count"])
	}
}

func TestCollectorRecordTrade(t *testing.T) {
	c := NewCollector()
	trade := &models.Trade{
		Instrument: "ETH-USD",
		Quantity:   models.FloatToQty(2.5),
	}
	c.RecordTrade("ETH-USD", trade)

	snap := c.Snapshot()
	instruments := snap["instruments"].(map[string]interface{})
	eth := instruments["ETH-USD"].(map[string]interface{})

	if eth["trades_executed"].(uint64) != 1 {
		t.Errorf("trades_executed = %v, want 1", eth["trades_executed"])
	}
}

func TestCollectorRecordCancellation(t *testing.T) {
	c := NewCollector()
	c.RecordCancellation("SOL-USD")
	c.RecordCancellation("SOL-USD")
	c.RecordCancellation("SOL-USD")

	snap := c.Snapshot()
	instruments := snap["instruments"].(map[string]interface{})
	sol := instruments["SOL-USD"].(map[string]interface{})

	if sol["cancellations"].(uint64) != 3 {
		t.Errorf("cancellations = %v, want 3", sol["cancellations"])
	}
}

func TestCollectorRecordBackPressure(t *testing.T) {
	c := NewCollector()
	c.RecordBackPressure("BNB-USD")

	snap := c.Snapshot()
	instruments := snap["instruments"].(map[string]interface{})
	bnb := instruments["BNB-USD"].(map[string]interface{})

	if bnb["back_pressure"].(uint64) != 1 {
		t.Errorf("back_pressure = %v, want 1", bnb["back_pressure"])
	}
}

func TestCollectorSnapshotUptime(t *testing.T) {
	c := NewCollector()
	time.Sleep(10 * time.Millisecond)
	snap := c.Snapshot()
	uptime := snap["uptime_seconds"].(float64)
	if uptime < 0.001 {
		t.Errorf("uptime_seconds = %f, expected > 0.001", uptime)
	}
}

func TestCollectorMultipleInstruments(t *testing.T) {
	c := NewCollector()
	c.RecordOrderProcessed("BTC-USD", time.Microsecond)
	c.RecordOrderProcessed("ETH-USD", time.Microsecond)
	c.RecordCancellation("SOL-USD")

	snap := c.Snapshot()
	instruments := snap["instruments"].(map[string]interface{})
	if len(instruments) != 3 {
		t.Errorf("expected 3 instruments, got %d", len(instruments))
	}
}

func TestCollectorConcurrentAccess(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.RecordOrderProcessed("BTC-USD", time.Microsecond)
		}()
	}
	wg.Wait()

	snap := c.Snapshot()
	instruments := snap["instruments"].(map[string]interface{})
	btc := instruments["BTC-USD"].(map[string]interface{})
	if btc["orders_processed"].(uint64) != 100 {
		t.Errorf("orders_processed = %v, want 100", btc["orders_processed"])
	}
}

func TestCollectorGetOrCreateIdempotent(t *testing.T) {
	c := NewCollector()
	m1 := c.getOrCreate("XRP-USD")
	m2 := c.getOrCreate("XRP-USD")
	if m1 != m2 {
		t.Error("getOrCreate should return same pointer for same instrument")
	}
}
