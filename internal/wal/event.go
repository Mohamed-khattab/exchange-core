package wal

// Event types for the write-ahead log.
const (
	EventOrderAdd       uint8 = 1
	EventOrderCancel    uint8 = 2
	EventStopActivation uint8 = 3
)
