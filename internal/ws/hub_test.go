package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func startTestHub(t *testing.T) (*Hub, context.CancelFunc) {
	t.Helper()
	hub := NewHub(100)
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	return hub, cancel
}

func connectClient(t *testing.T, hub *Hub) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleUpgrade(w, r)
	}))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, srv
}

func readMsg(t *testing.T, conn *websocket.Conn, timeout time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

func TestHubClientConnects(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)
	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}
}

func TestHubSubscribeAndReceive(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe
	sub := `{"type":"subscribe","channels":["trades"],"instruments":["BTC-USD"]}`
	conn.Write(context.Background(), websocket.MessageText, []byte(sub))

	// Read subscription confirmation
	msg := readMsg(t, conn, 2*time.Second)
	var resp ServerMessage
	json.Unmarshal(msg, &resp)
	if resp.Type != "subscribed" {
		t.Errorf("expected subscribed, got %s", resp.Type)
	}

	// Publish a trade event
	hub.Publish(&Event{
		Type:       "trades",
		Instrument: "BTC-USD",
		Data:       map[string]interface{}{"price": 50000.0},
	})

	// Should receive it
	msg = readMsg(t, conn, 2*time.Second)
	var evt Event
	json.Unmarshal(msg, &evt)
	if evt.Type != "trades" {
		t.Errorf("expected trade event, got %s", evt.Type)
	}
	if evt.Instrument != "BTC-USD" {
		t.Errorf("instrument = %s", evt.Instrument)
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe
	conn.Write(context.Background(), websocket.MessageText,
		[]byte(`{"type":"subscribe","channels":["trades"],"instruments":["BTC-USD"]}`))
	readMsg(t, conn, 2*time.Second) // subscribed

	// Unsubscribe
	conn.Write(context.Background(), websocket.MessageText,
		[]byte(`{"type":"unsubscribe","channels":["trades"],"instruments":["BTC-USD"]}`))
	readMsg(t, conn, 2*time.Second) // unsubscribed

	// Publish -- should NOT receive
	hub.Publish(&Event{Type: "trades", Instrument: "BTC-USD", Data: "test"})

	ctx, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Error("should not have received event after unsubscribe")
	}
}

func TestHubDifferentInstruments(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe to BTC-USD only
	conn.Write(context.Background(), websocket.MessageText,
		[]byte(`{"type":"subscribe","channels":["trades"],"instruments":["BTC-USD"]}`))
	readMsg(t, conn, 2*time.Second)

	// Publish ETH-USD event -- should NOT receive
	hub.Publish(&Event{Type: "trades", Instrument: "ETH-USD", Data: "eth"})

	ctx, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Error("should not receive events for unsubscribed instrument")
	}
}

func TestHubPing(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`))
	msg := readMsg(t, conn, 2*time.Second)
	var resp ServerMessage
	json.Unmarshal(msg, &resp)
	if resp.Type != "pong" {
		t.Errorf("expected pong, got %s", resp.Type)
	}
}

func TestHubInvalidMessage(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(context.Background(), websocket.MessageText, []byte(`{bad json`))
	msg := readMsg(t, conn, 2*time.Second)
	var resp ServerMessage
	json.Unmarshal(msg, &resp)
	if resp.Type != "error" {
		t.Errorf("expected error, got %s", resp.Type)
	}
}

func TestHubUnknownMessageType(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"unknown"}`))
	msg := readMsg(t, conn, 2*time.Second)
	var resp ServerMessage
	json.Unmarshal(msg, &resp)
	if resp.Type != "error" {
		t.Errorf("expected error, got %s", resp.Type)
	}
}

func TestHubMultipleChannels(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()

	conn, srv := connectClient(t, hub)
	defer srv.Close()
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(context.Background(), websocket.MessageText,
		[]byte(`{"type":"subscribe","channels":["trades","ticker"],"instruments":["BTC-USD"]}`))
	readMsg(t, conn, 2*time.Second)

	// Trade event
	hub.Publish(&Event{Type: "trades", Instrument: "BTC-USD", Data: "t"})
	msg := readMsg(t, conn, 2*time.Second)
	var evt Event
	json.Unmarshal(msg, &evt)
	if evt.Type != "trades" {
		t.Errorf("expected trades, got %s", evt.Type)
	}

	// Ticker event
	hub.Publish(&Event{Type: "ticker", Instrument: "BTC-USD", Data: "tk"})
	msg = readMsg(t, conn, 2*time.Second)
	json.Unmarshal(msg, &evt)
	if evt.Type != "ticker" {
		t.Errorf("expected ticker, got %s", evt.Type)
	}

	// Book event (not subscribed)
	hub.Publish(&Event{Type: "book", Instrument: "BTC-USD", Data: "bk"})
	ctx, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Error("should not receive book events")
	}
}

func TestHubPublishNonBlocking(t *testing.T) {
	hub := NewHub(10)
	// Publish without any clients or Run() -- should not block
	for i := 0; i < 20000; i++ {
		hub.Publish(&Event{Type: "trade", Instrument: "BTC-USD", Data: i})
	}
}

func TestHubMaxClients(t *testing.T) {
	hub, cancel := startTestHub(t)
	defer cancel()
	hub.maxClients = 1

	conn1, srv1 := connectClient(t, hub)
	defer srv1.Close()
	defer conn1.Close(websocket.StatusNormalClosure, "")
	time.Sleep(50 * time.Millisecond)

	// Second connection should be rejected
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleUpgrade(w, r)
	}))
	defer srv2.Close()
	wsURL := "ws" + strings.TrimPrefix(srv2.URL, "http")
	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Error("expected rejection for max clients")
	}
	if resp != nil && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}

func TestNewHubDefaults(t *testing.T) {
	hub := NewHub(0) // should default to 1000
	if hub.maxClients != 1000 {
		t.Errorf("maxClients = %d, want 1000", hub.maxClients)
	}
}

func TestMarshalEvent(t *testing.T) {
	evt := &Event{Type: "trade", Instrument: "BTC-USD", Data: map[string]float64{"price": 50000}}
	data := MarshalEvent(evt)
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
	var decoded Event
	json.Unmarshal(data, &decoded)
	if decoded.Type != "trade" {
		t.Errorf("type = %s", decoded.Type)
	}
}

func TestMarshalServerMessage(t *testing.T) {
	msg := &ServerMessage{Type: "subscribed", Channels: []string{"trades"}, Instruments: []string{"BTC-USD"}}
	data := MarshalServerMessage(msg)
	var decoded ServerMessage
	json.Unmarshal(data, &decoded)
	if decoded.Type != "subscribed" {
		t.Errorf("type = %s", decoded.Type)
	}
}
