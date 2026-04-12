package session

import (
	"testing"
)

func TestSessionPhaseString(t *testing.T) {
	cases := map[SessionPhase]string{
		PhasePreOpen:      "PRE_OPEN",
		PhaseAuctionOpen:  "AUCTION_OPEN",
		PhaseContinuous:   "CONTINUOUS",
		PhaseAuctionClose: "AUCTION_CLOSE",
		PhaseClosed:       "CLOSED",
		SessionPhase(99):  "UNKNOWN",
	}
	for phase, want := range cases {
		if got := phase.String(); got != want {
			t.Errorf("SessionPhase(%d).String() = %s, want %s", phase, got, want)
		}
	}
}

func TestParsePhase(t *testing.T) {
	valid := map[string]SessionPhase{
		"PRE_OPEN":      PhasePreOpen,
		"AUCTION_OPEN":  PhaseAuctionOpen,
		"CONTINUOUS":    PhaseContinuous,
		"AUCTION_CLOSE": PhaseAuctionClose,
		"CLOSED":        PhaseClosed,
	}
	for s, want := range valid {
		got, err := ParsePhase(s)
		if err != nil {
			t.Errorf("ParsePhase(%q) error: %v", s, err)
		}
		if got != want {
			t.Errorf("ParsePhase(%q) = %d, want %d", s, got, want)
		}
	}

	_, err := ParsePhase("INVALID")
	if err == nil {
		t.Error("expected error for INVALID phase")
	}
}

func TestValidTransitions(t *testing.T) {
	valid := [][2]SessionPhase{
		{PhasePreOpen, PhaseAuctionOpen},
		{PhasePreOpen, PhaseContinuous},
		{PhaseAuctionOpen, PhaseContinuous},
		{PhaseContinuous, PhaseAuctionClose},
		{PhaseContinuous, PhaseClosed},
		{PhaseAuctionClose, PhaseClosed},
		{PhaseClosed, PhasePreOpen},
	}
	for _, pair := range valid {
		if !IsValidTransition(pair[0], pair[1]) {
			t.Errorf("%s -> %s should be valid", pair[0], pair[1])
		}
	}

	invalid := [][2]SessionPhase{
		{PhasePreOpen, PhaseClosed},
		{PhaseAuctionOpen, PhaseAuctionClose},
		{PhaseContinuous, PhasePreOpen},
		{PhaseClosed, PhaseContinuous},
		{PhaseClosed, PhaseAuctionOpen},
	}
	for _, pair := range invalid {
		if IsValidTransition(pair[0], pair[1]) {
			t.Errorf("%s -> %s should be invalid", pair[0], pair[1])
		}
	}
}

func TestInstrumentSessionTransition(t *testing.T) {
	s := NewInstrumentSession("BTC-USD", PhasePreOpen)

	if s.Phase() != PhasePreOpen {
		t.Errorf("initial phase = %s", s.Phase())
	}

	if err := s.Transition(PhaseAuctionOpen); err != nil {
		t.Fatalf("PRE_OPEN -> AUCTION_OPEN: %v", err)
	}
	if s.Phase() != PhaseAuctionOpen {
		t.Errorf("phase = %s, want AUCTION_OPEN", s.Phase())
	}

	if err := s.Transition(PhaseContinuous); err != nil {
		t.Fatalf("AUCTION_OPEN -> CONTINUOUS: %v", err)
	}

	// Invalid transition
	if err := s.Transition(PhasePreOpen); err == nil {
		t.Error("CONTINUOUS -> PRE_OPEN should fail")
	}
}

func TestForcePhase(t *testing.T) {
	s := NewInstrumentSession("BTC-USD", PhasePreOpen)
	s.ForcePhase(PhaseClosed) // skip validation
	if s.Phase() != PhaseClosed {
		t.Errorf("phase = %s, want CLOSED", s.Phase())
	}
}

func TestIsAuctionPhase(t *testing.T) {
	if !IsAuctionPhase(PhaseAuctionOpen) {
		t.Error("AUCTION_OPEN should be auction phase")
	}
	if !IsAuctionPhase(PhaseAuctionClose) {
		t.Error("AUCTION_CLOSE should be auction phase")
	}
	if IsAuctionPhase(PhaseContinuous) {
		t.Error("CONTINUOUS should not be auction phase")
	}
}

func TestAcceptsOrders(t *testing.T) {
	if !AcceptsOrders(PhasePreOpen) {
		t.Error("PRE_OPEN accepts orders")
	}
	if !AcceptsOrders(PhaseContinuous) {
		t.Error("CONTINUOUS accepts orders")
	}
	if AcceptsOrders(PhaseClosed) {
		t.Error("CLOSED should not accept orders")
	}
}

func TestMatchingEnabled(t *testing.T) {
	if !MatchingEnabled(PhaseContinuous) {
		t.Error("CONTINUOUS should have matching")
	}
	if MatchingEnabled(PhaseAuctionOpen) {
		t.Error("AUCTION_OPEN should not have continuous matching")
	}
	if MatchingEnabled(PhasePreOpen) {
		t.Error("PRE_OPEN should not have matching")
	}
}

func TestSessionStatus(t *testing.T) {
	s := NewInstrumentSession("ETH-USD", PhaseContinuous)
	status := s.Status()
	if status["instrument"] != "ETH-USD" {
		t.Errorf("instrument = %v", status["instrument"])
	}
	if status["phase"] != "CONTINUOUS" {
		t.Errorf("phase = %v", status["phase"])
	}
}

func TestFullSessionLifecycle(t *testing.T) {
	s := NewInstrumentSession("BTC-USD", PhaseClosed)

	transitions := []SessionPhase{
		PhasePreOpen, PhaseAuctionOpen, PhaseContinuous,
		PhaseAuctionClose, PhaseClosed,
	}
	for _, next := range transitions {
		if err := s.Transition(next); err != nil {
			t.Fatalf("%s -> %s: %v", s.Phase(), next, err)
		}
	}
}
