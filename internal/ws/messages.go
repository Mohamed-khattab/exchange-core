package ws

import "encoding/json"

// Event is a server-side event to be dispatched to WebSocket clients.
type Event struct {
	Type       string      `json:"type"`       // "trade", "book", "ticker"
	Instrument string      `json:"instrument"`
	Data       interface{} `json:"data"`
}

// ClientMessage is a message from a WebSocket client.
type ClientMessage struct {
	Type        string   `json:"type"`        // "subscribe", "unsubscribe", "ping"
	Channels    []string `json:"channels"`    // ["trades", "book", "ticker"]
	Instruments []string `json:"instruments"` // ["BTC-USD"]
}

// ServerMessage is a control message sent to the client.
type ServerMessage struct {
	Type        string   `json:"type"`                  // "subscribed", "unsubscribed", "pong", "error"
	Channels    []string `json:"channels,omitempty"`
	Instruments []string `json:"instruments,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// MarshalEvent serializes an event to JSON bytes.
func MarshalEvent(e *Event) []byte {
	data, _ := json.Marshal(e)
	return data
}

// MarshalServerMessage serializes a server message to JSON bytes.
func MarshalServerMessage(m *ServerMessage) []byte {
	data, _ := json.Marshal(m)
	return data
}
