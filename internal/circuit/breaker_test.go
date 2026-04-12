package circuit

import (
	"testing"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

func testCfg() Config {
	return Config{
		Enabled:         true,
		PriceBandPct:    0.05, // ±5%
		VelocityPct:     0.10, // 10% in window
		VelocityWindow:  1 * time.Minute,
		MaxNotional:     1_000_000_00000000, // 1M in fixed-point
		AutoResumeAfter: 100 * time.Millisecond,
		PreOpenDuration: 50 * time.Millisecond,
	}
}

func testOrder(price float64, qty float64) *models.Order {
	return &models.Order{
		Price:    models.FloatToPrice(price),
		Quantity: models.FloatToQty(qty),
		Side:     models.SideBuy,
		Type:     models.OrderTypeLimit,
	}
}

func TestCheckOrderContinuousAllowed(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.referencePrice = models.FloatToPrice(50000)

	err := b.CheckOrder(testOrder(50000, 1.0))
	if err != nil {
		t.Errorf("order within band should be allowed: %v", err)
	}
}

func TestCheckOrderOutsideBand(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.referencePrice = models.FloatToPrice(50000)

	// 10% away (band is 5%)
	err := b.CheckOrder(testOrder(55001, 1.0))
	if err == nil {
		t.Error("order outside band should be rejected")
	}
}

func TestCheckOrderWithinBandEdge(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.referencePrice = models.FloatToPrice(50000)

	// Exactly 5% up = 52500
	err := b.CheckOrder(testOrder(52500, 0.1))
	if err != nil {
		t.Errorf("order at band edge should be allowed: %v", err)
	}
}

func TestCheckOrderHalted(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.Halt("test halt")

	err := b.CheckOrder(testOrder(50000, 1.0))
	if err == nil {
		t.Error("order should be rejected when halted")
	}
}

func TestCheckOrderPreOpen(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.mu.Lock()
	b.state = StatePreOpen
	b.mu.Unlock()

	err := b.CheckOrder(testOrder(50000, 1.0))
	if err == nil {
		t.Error("order should be rejected during pre-open")
	}
}

func TestCheckOrderFatFinger(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.referencePrice = models.FloatToPrice(50000)

	// 50000 * 100 = 5M notional, exceeds 1M max
	err := b.CheckOrder(testOrder(50000, 100.0))
	if err == nil {
		t.Error("fat-finger order should be rejected")
	}
}

func TestCheckOrderZeroReferenceSkipsBand(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	// No reference price set (first trade)

	err := b.CheckOrder(testOrder(99999, 1.0))
	if err != nil {
		t.Errorf("should skip band check with zero reference: %v", err)
	}
}

func TestCheckOrderMarketSkipsBand(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.referencePrice = models.FloatToPrice(50000)

	// Market orders have price=0, should skip band check
	mkt := &models.Order{
		Price:    0,
		Quantity: models.FloatToQty(1.0),
		Side:     models.SideBuy,
		Type:     models.OrderTypeMarket,
	}
	err := b.CheckOrder(mkt)
	if err != nil {
		t.Errorf("market order should skip band check: %v", err)
	}
}

func TestRecordTradeUpdatesReference(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())

	b.RecordTrade(models.FloatToPrice(50000), time.Now())
	if b.ReferencePrice() != models.FloatToPrice(50000) {
		t.Errorf("reference = %d, want %d", b.ReferencePrice(), models.FloatToPrice(50000))
	}
}

func TestVelocityTriggerHalts(t *testing.T) {
	cfg := testCfg()
	cfg.VelocityWindow = 5 * time.Second
	b := NewInstrumentBreaker("BTC-USD", cfg)

	now := time.Now()
	b.RecordTrade(models.FloatToPrice(50000), now)

	// 15% move (threshold is 10%)
	tripped := b.RecordTrade(models.FloatToPrice(57500), now.Add(1*time.Second))
	if !tripped {
		t.Error("expected velocity trigger")
	}
	if b.State() != StateHalted {
		t.Errorf("state = %s, want HALTED", b.State())
	}
}

func TestVelocityNoTriggerWithinLimit(t *testing.T) {
	cfg := testCfg()
	cfg.VelocityWindow = 5 * time.Second
	b := NewInstrumentBreaker("BTC-USD", cfg)

	now := time.Now()
	b.RecordTrade(models.FloatToPrice(50000), now)

	// 5% move (threshold is 10%)
	tripped := b.RecordTrade(models.FloatToPrice(52500), now.Add(1*time.Second))
	if tripped {
		t.Error("should not trigger for 5% move (threshold 10%)")
	}
	if b.State() != StateContinuous {
		t.Errorf("state = %s, want CONTINUOUS", b.State())
	}
}

func TestManualHaltAndResume(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())

	b.Halt("manual halt")
	if b.State() != StateHalted {
		t.Errorf("state = %s, want HALTED", b.State())
	}
	if b.HaltReason() != "manual halt" {
		t.Errorf("reason = %s", b.HaltReason())
	}

	b.Resume()
	if b.State() != StateContinuous {
		t.Errorf("state = %s, want CONTINUOUS after resume", b.State())
	}
	if b.HaltReason() != "" {
		t.Errorf("reason should be empty after resume")
	}
}

func TestAutoResumeWithPreOpen(t *testing.T) {
	cfg := testCfg()
	cfg.AutoResumeAfter = 50 * time.Millisecond
	cfg.PreOpenDuration = 50 * time.Millisecond
	b := NewInstrumentBreaker("BTC-USD", cfg)

	b.Halt("velocity")

	// Wait for auto-resume to transition to PRE_OPEN
	time.Sleep(80 * time.Millisecond)
	if b.State() != StatePreOpen {
		t.Errorf("expected PRE_OPEN after auto-resume, got %s", b.State())
	}

	// Wait for pre-open to transition to CONTINUOUS
	time.Sleep(80 * time.Millisecond)
	if b.State() != StateContinuous {
		t.Errorf("expected CONTINUOUS after pre-open, got %s", b.State())
	}
}

func TestAutoResumeWithoutPreOpen(t *testing.T) {
	cfg := testCfg()
	cfg.AutoResumeAfter = 50 * time.Millisecond
	cfg.PreOpenDuration = 0
	b := NewInstrumentBreaker("BTC-USD", cfg)

	b.Halt("test")

	time.Sleep(80 * time.Millisecond)
	if b.State() != StateContinuous {
		t.Errorf("expected CONTINUOUS after auto-resume (no pre-open), got %s", b.State())
	}
}

func TestStatus(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.RecordTrade(models.FloatToPrice(50000), time.Now())

	status := b.Status()
	if status["instrument"] != "BTC-USD" {
		t.Errorf("instrument = %v", status["instrument"])
	}
	if status["state"] != "CONTINUOUS" {
		t.Errorf("state = %v", status["state"])
	}
}

func TestTradingStateString(t *testing.T) {
	cases := map[TradingState]string{
		StateContinuous: "CONTINUOUS",
		StateHalted:     "HALTED",
		StatePreOpen:    "PRE_OPEN",
		TradingState(99): "UNKNOWN",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("TradingState(%d).String() = %s, want %s", state, got, want)
		}
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry([]string{"BTC-USD", "ETH-USD"}, testCfg())

	b := r.Get("BTC-USD")
	if b == nil {
		t.Fatal("expected breaker for BTC-USD")
	}
	if b.instrument != "BTC-USD" {
		t.Errorf("instrument = %s", b.instrument)
	}

	if r.Get("UNKNOWN") != nil {
		t.Error("expected nil for unknown instrument")
	}
}

func TestDisabledBreakerAllowsEverything(t *testing.T) {
	cfg := Config{Enabled: false}
	b := NewInstrumentBreaker("BTC-USD", cfg)

	// Even with no reference price, should pass
	err := b.CheckOrder(testOrder(999999, 999))
	if err != nil {
		t.Errorf("disabled breaker should allow everything: %v", err)
	}
}

func TestResumeWhileContinuousIsNoop(t *testing.T) {
	b := NewInstrumentBreaker("BTC-USD", testCfg())
	b.Resume() // should not panic
	if b.State() != StateContinuous {
		t.Errorf("state = %s", b.State())
	}
}
