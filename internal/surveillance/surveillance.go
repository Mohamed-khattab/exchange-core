// Package surveillance implements market manipulation detection.
// Detectors run asynchronously — they never block the matching engine.
package surveillance

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// EventType classifies surveillance events.
type EventType int

const (
	EventOrderPlaced    EventType = iota
	EventOrderCancelled
	EventOrderAmended
	EventOrderRejected
	EventTradeExecuted
	EventSTPTriggered
)

// Event represents an order lifecycle event for surveillance analysis.
type Event struct {
	Type       EventType
	Timestamp  time.Time
	Order      *models.Order
	Trade      *models.Trade
	Instrument string
	ClientID   string
	Reason     string
}

// Alert represents a surveillance finding.
type Alert struct {
	DetectorName string      `json:"detector"`
	Severity     string      `json:"severity"` // "INFO", "WARNING", "CRITICAL"
	Instrument   string      `json:"instrument"`
	ClientID     string      `json:"client_id"`
	Description  string      `json:"description"`
	Timestamp    time.Time   `json:"timestamp"`
	Evidence     interface{} `json:"evidence,omitempty"`
}

// Detector analyzes surveillance events and produces alerts.
type Detector interface {
	Name() string
	Enabled() bool
	OnEvent(event *Event) []Alert
}

// Monitor manages detectors and distributes events asynchronously.
type Monitor struct {
	detectors []Detector
	eventCh   chan *Event
	alertCh   chan Alert
	alerts    []Alert // bounded ring buffer of recent alerts
	alertMu   sync.RWMutex
	maxAlerts int
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewMonitor creates a surveillance monitor.
func NewMonitor(detectors []Detector, maxAlerts int) *Monitor {
	if maxAlerts <= 0 {
		maxAlerts = 10_000
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Monitor{
		detectors: detectors,
		eventCh:   make(chan *Event, 50_000),
		alertCh:   make(chan Alert, 10_000),
		alerts:    make([]Alert, 0, maxAlerts),
		maxAlerts: maxAlerts,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Run starts the monitor event loop. Blocks until stopped.
func (m *Monitor) Run() {
	log.Printf("[surveillance] monitor started with %d detectors", len(m.detectors))
	for {
		select {
		case event := <-m.eventCh:
			m.dispatch(event)
		case <-m.ctx.Done():
			return
		}
	}
}

// Stop shuts down the monitor.
func (m *Monitor) Stop() {
	m.cancel()
}

// EventChannel returns the channel for sending events to the monitor.
func (m *Monitor) EventChannel() chan<- *Event {
	return m.eventCh
}

// AlertChannel returns the channel for consuming alerts.
func (m *Monitor) AlertChannel() <-chan Alert {
	return m.alertCh
}

// RecentAlerts returns a copy of recent alerts.
func (m *Monitor) RecentAlerts(instrument string, since time.Time) []Alert {
	m.alertMu.RLock()
	defer m.alertMu.RUnlock()

	var result []Alert
	for _, a := range m.alerts {
		if instrument != "" && a.Instrument != instrument {
			continue
		}
		if !since.IsZero() && a.Timestamp.Before(since) {
			continue
		}
		result = append(result, a)
	}
	return result
}

func (m *Monitor) dispatch(event *Event) {
	for _, d := range m.detectors {
		if !d.Enabled() {
			continue
		}
		alerts := d.OnEvent(event)
		for _, alert := range alerts {
			m.recordAlert(alert)
			// Non-blocking send to alertCh
			select {
			case m.alertCh <- alert:
			default:
			}
		}
	}
}

func (m *Monitor) recordAlert(alert Alert) {
	m.alertMu.Lock()
	defer m.alertMu.Unlock()
	if len(m.alerts) >= m.maxAlerts {
		// Drop oldest (shift left)
		m.alerts = m.alerts[1:]
	}
	m.alerts = append(m.alerts, alert)
	log.Printf("[surveillance] ALERT [%s] %s: %s (client=%s instrument=%s)",
		alert.Severity, alert.DetectorName, alert.Description, alert.ClientID, alert.Instrument)
}
