// Package circuit implements per-instrument trading circuit breakers.
// Circuit breakers halt trading when price moves exceed configured thresholds.
package circuit

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// TradingState represents the current state of trading for an instrument.
type TradingState int

const (
	StateContinuous TradingState = iota
	StateHalted
	StatePreOpen
)

func (s TradingState) String() string {
	switch s {
	case StateContinuous:
		return "CONTINUOUS"
	case StateHalted:
		return "HALTED"
	case StatePreOpen:
		return "PRE_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Config holds circuit breaker configuration.
type Config struct {
	Enabled         bool
	PriceBandPct    float64       // e.g., 0.05 = ±5% from reference price
	VelocityPct     float64       // e.g., 0.10 = 10% max move in velocity window
	VelocityWindow  time.Duration // e.g., 1 minute
	MaxNotional     int64         // max order value (price * qty) in fixed-point
	AutoResumeAfter time.Duration // how long a halt lasts before auto-resume
	PreOpenDuration time.Duration // pre-open phase duration before continuous
}

type pricePoint struct {
	price     int64
	timestamp time.Time
}

// InstrumentBreaker manages circuit breaker state for a single instrument.
type InstrumentBreaker struct {
	mu             sync.RWMutex
	instrument     string
	state          TradingState
	referencePrice int64
	haltedAt       time.Time
	haltReason     string
	resumeTimer    *time.Timer
	preOpenTimer   *time.Timer

	// Circular buffer for velocity checks
	priceWindow [256]pricePoint
	windowHead  int
	windowCount int

	cfg Config
}

// NewInstrumentBreaker creates a circuit breaker for the given instrument.
func NewInstrumentBreaker(instrument string, cfg Config) *InstrumentBreaker {
	return &InstrumentBreaker{
		instrument: instrument,
		state:      StateContinuous,
		cfg:        cfg,
	}
}

// State returns the current trading state.
func (b *InstrumentBreaker) State() TradingState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// ReferencePrice returns the current reference price.
func (b *InstrumentBreaker) ReferencePrice() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.referencePrice
}

// HaltReason returns the reason for the current halt (empty if not halted).
func (b *InstrumentBreaker) HaltReason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.haltReason
}

// Status returns a map of current breaker status for API responses.
func (b *InstrumentBreaker) Status() map[string]interface{} {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return map[string]interface{}{
		"instrument":      b.instrument,
		"state":           b.state.String(),
		"reference_price": models.PriceToFloat(b.referencePrice),
		"halt_reason":     b.haltReason,
	}
}

// CheckOrder validates an order against circuit breaker rules.
// Returns nil if the order is allowed, an error if rejected.
// Cancellations are always allowed (pass order=nil or check before calling).
func (b *InstrumentBreaker) CheckOrder(order *models.Order) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Halted or pre-open: reject new orders
	if b.state != StateContinuous {
		return fmt.Errorf("trading %s for %s", b.state.String(), b.instrument)
	}

	// Price band check (only for limit-like orders with a price)
	if b.referencePrice > 0 && order.Price > 0 && b.cfg.PriceBandPct > 0 {
		ref := float64(b.referencePrice)
		price := float64(order.Price)
		deviation := math.Abs(price-ref) / ref
		if deviation > b.cfg.PriceBandPct {
			return fmt.Errorf("price %.8f outside allowed band (±%.1f%% from %.8f)",
				models.PriceToFloat(order.Price),
				b.cfg.PriceBandPct*100,
				models.PriceToFloat(b.referencePrice))
		}
	}

	// Fat-finger check
	if b.cfg.MaxNotional > 0 && order.Price > 0 {
		notional := order.Price * int64(order.Quantity) / models.PriceScale
		if notional > b.cfg.MaxNotional {
			return fmt.Errorf("order notional exceeds maximum allowed")
		}
	}

	return nil
}

// RecordTrade records a trade and checks velocity triggers.
// Returns true if the breaker tripped (state changed to HALTED).
func (b *InstrumentBreaker) RecordTrade(price int64, timestamp time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.referencePrice = price

	// Add to price window
	idx := b.windowHead
	b.priceWindow[idx] = pricePoint{price: price, timestamp: timestamp}
	b.windowHead = (b.windowHead + 1) % len(b.priceWindow)
	if b.windowCount < len(b.priceWindow) {
		b.windowCount++
	}

	// Velocity check
	if b.cfg.VelocityPct > 0 && b.cfg.VelocityWindow > 0 && b.windowCount > 1 {
		cutoff := timestamp.Add(-b.cfg.VelocityWindow)
		oldestPrice := int64(0)

		// Find oldest price within the velocity window
		for i := 0; i < b.windowCount; i++ {
			p := b.priceWindow[(b.windowHead-b.windowCount+i+len(b.priceWindow))%len(b.priceWindow)]
			if p.timestamp.Before(cutoff) {
				continue
			}
			if oldestPrice == 0 {
				oldestPrice = p.price
			}
		}

		if oldestPrice > 0 {
			velocity := math.Abs(float64(price)-float64(oldestPrice)) / float64(oldestPrice)
			if velocity > b.cfg.VelocityPct {
				b.trip(fmt.Sprintf("velocity breach: %.2f%% in %s", velocity*100, b.cfg.VelocityWindow))
				return true
			}
		}
	}

	return false
}

// Halt manually halts trading for the instrument.
func (b *InstrumentBreaker) Halt(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trip(reason)
}

// Resume manually resumes trading (immediately to CONTINUOUS).
func (b *InstrumentBreaker) Resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelTimers()
	b.state = StateContinuous
	b.haltReason = ""
}

func (b *InstrumentBreaker) trip(reason string) {
	b.cancelTimers()
	b.state = StateHalted
	b.haltedAt = time.Now()
	b.haltReason = reason

	if b.cfg.AutoResumeAfter > 0 {
		b.resumeTimer = time.AfterFunc(b.cfg.AutoResumeAfter, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.state != StateHalted {
				return
			}
			if b.cfg.PreOpenDuration > 0 {
				b.state = StatePreOpen
				b.preOpenTimer = time.AfterFunc(b.cfg.PreOpenDuration, func() {
					b.mu.Lock()
					defer b.mu.Unlock()
					if b.state == StatePreOpen {
						b.state = StateContinuous
						b.haltReason = ""
					}
				})
			} else {
				b.state = StateContinuous
				b.haltReason = ""
			}
		})
	}
}

func (b *InstrumentBreaker) cancelTimers() {
	if b.resumeTimer != nil {
		b.resumeTimer.Stop()
		b.resumeTimer = nil
	}
	if b.preOpenTimer != nil {
		b.preOpenTimer.Stop()
		b.preOpenTimer = nil
	}
}

// Registry manages circuit breakers for all instruments.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*InstrumentBreaker
	cfg      Config
}

// NewRegistry creates a registry with the given default config.
func NewRegistry(instruments []string, cfg Config) *Registry {
	r := &Registry{
		breakers: make(map[string]*InstrumentBreaker, len(instruments)),
		cfg:      cfg,
	}
	for _, inst := range instruments {
		r.breakers[inst] = NewInstrumentBreaker(inst, cfg)
	}
	return r
}

// Get returns the circuit breaker for an instrument.
func (r *Registry) Get(instrument string) *InstrumentBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.breakers[instrument]
}
