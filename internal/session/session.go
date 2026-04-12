// Package session manages per-instrument trading session phases.
// Sessions control when matching occurs (continuous vs auction vs closed).
package session

import (
	"fmt"
	"sync"
)

// SessionPhase represents the current trading phase for an instrument.
type SessionPhase int

const (
	PhasePreOpen      SessionPhase = iota // Orders accepted, no matching
	PhaseAuctionOpen                      // Opening auction: orders queued, indicative price
	PhaseContinuous                       // Normal continuous matching
	PhaseAuctionClose                     // Closing auction: orders queued, indicative price
	PhaseClosed                           // Market closed: nothing accepted
)

func (p SessionPhase) String() string {
	switch p {
	case PhasePreOpen:
		return "PRE_OPEN"
	case PhaseAuctionOpen:
		return "AUCTION_OPEN"
	case PhaseContinuous:
		return "CONTINUOUS"
	case PhaseAuctionClose:
		return "AUCTION_CLOSE"
	case PhaseClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// ParsePhase converts a string to a SessionPhase.
func ParsePhase(s string) (SessionPhase, error) {
	switch s {
	case "PRE_OPEN":
		return PhasePreOpen, nil
	case "AUCTION_OPEN":
		return PhaseAuctionOpen, nil
	case "CONTINUOUS":
		return PhaseContinuous, nil
	case "AUCTION_CLOSE":
		return PhaseAuctionClose, nil
	case "CLOSED":
		return PhaseClosed, nil
	default:
		return 0, fmt.Errorf("unknown session phase: %s", s)
	}
}

// validTransitions defines allowed phase transitions.
var validTransitions = map[SessionPhase][]SessionPhase{
	PhasePreOpen:      {PhaseAuctionOpen, PhaseContinuous}, // can skip auction
	PhaseAuctionOpen:  {PhaseContinuous},
	PhaseContinuous:   {PhaseAuctionClose, PhaseClosed}, // can skip closing auction
	PhaseAuctionClose: {PhaseClosed},
	PhaseClosed:       {PhasePreOpen},
}

// IsValidTransition checks if transitioning from one phase to another is allowed.
func IsValidTransition(from, to SessionPhase) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}

// IsAuctionPhase returns true if the phase is an auction phase.
func IsAuctionPhase(phase SessionPhase) bool {
	return phase == PhaseAuctionOpen || phase == PhaseAuctionClose
}

// AcceptsOrders returns true if new orders are accepted in this phase.
func AcceptsOrders(phase SessionPhase) bool {
	return phase != PhaseClosed
}

// AcceptsCancellations returns true if cancellations are accepted.
func AcceptsCancellations(phase SessionPhase) bool {
	return phase != PhaseClosed
}

// MatchingEnabled returns true if continuous matching should occur.
func MatchingEnabled(phase SessionPhase) bool {
	return phase == PhaseContinuous
}

// InstrumentSession manages the trading session for a single instrument.
type InstrumentSession struct {
	mu         sync.RWMutex
	instrument string
	phase      SessionPhase
}

// NewInstrumentSession creates a session starting in the given phase.
func NewInstrumentSession(instrument string, initialPhase SessionPhase) *InstrumentSession {
	return &InstrumentSession{
		instrument: instrument,
		phase:      initialPhase,
	}
}

// Phase returns the current session phase.
func (s *InstrumentSession) Phase() SessionPhase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.phase
}

// Transition moves to a new phase. Returns an error if the transition is invalid.
func (s *InstrumentSession) Transition(to SessionPhase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !IsValidTransition(s.phase, to) {
		return fmt.Errorf("invalid session transition: %s -> %s", s.phase, to)
	}
	s.phase = to
	return nil
}

// ForcePhase sets the phase without transition validation (used during WAL replay).
func (s *InstrumentSession) ForcePhase(phase SessionPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phase
}

// Status returns session info for API responses.
func (s *InstrumentSession) Status() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]interface{}{
		"instrument": s.instrument,
		"phase":      s.phase.String(),
	}
}
