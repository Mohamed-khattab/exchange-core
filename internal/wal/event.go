package wal

// Event types for the write-ahead log.
const (
	EventOrderAdd       uint8 = 1
	EventOrderCancel    uint8 = 2
	EventStopActivation uint8 = 3
	EventOrderAmend        uint8 = 4
	EventMassCancel        uint8 = 5
	EventSessionTransition uint8 = 6
	EventAuctionUncross    uint8 = 7
)
