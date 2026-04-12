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
	EventOrderRejected     uint8 = 8
	EventTradeExecuted     uint8 = 9
)

// Rejection reason codes for EventOrderRejected.
const (
	RejectMarketClosed   uint16 = 1
	RejectCircuitBreaker uint16 = 2
	RejectQueueFull      uint16 = 3
	RejectWALWriteFailed uint16 = 4
	RejectInvalidOrder   uint16 = 5
	RejectOTRThrottled   uint16 = 6
)
