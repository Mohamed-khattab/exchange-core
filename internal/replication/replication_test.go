package replication

import (
	"bytes"
	"testing"
	"time"
)

func TestHandshakeRoundTrip(t *testing.T) {
	instruments := []string{"BTC-USD", "ETH-USD", "SOL-USD"}

	var buf bytes.Buffer
	if err := WriteHandshake(&buf, instruments); err != nil {
		t.Fatalf("WriteHandshake: %v", err)
	}

	got, err := ReadHandshake(&buf)
	if err != nil {
		t.Fatalf("ReadHandshake: %v", err)
	}
	if len(got) != len(instruments) {
		t.Fatalf("got %d instruments, want %d", len(got), len(instruments))
	}
	for i, inst := range got {
		if inst != instruments[i] {
			t.Errorf("instrument[%d] = %s, want %s", i, inst, instruments[i])
		}
	}
}

func TestResponseRoundTrip(t *testing.T) {
	var buf bytes.Buffer

	WriteResponse(&buf, true)
	accepted, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if !accepted {
		t.Error("expected accepted=true")
	}

	buf.Reset()
	WriteResponse(&buf, false)
	accepted, err = ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if accepted {
		t.Error("expected accepted=false")
	}
}

func TestEventRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	record := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	if err := WriteEvent(&buf, "BTC-USD", record); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	msgType, instrument, data, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgWALEvent {
		t.Errorf("msgType = %d, want %d", msgType, MsgWALEvent)
	}
	if instrument != "BTC-USD" {
		t.Errorf("instrument = %s", instrument)
	}
	if !bytes.Equal(data, record) {
		t.Errorf("data mismatch")
	}
}

func TestHeartbeatRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	WriteHeartbeat(&buf)

	msgType, _, _, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgHeartbeat {
		t.Errorf("msgType = %d, want %d", msgType, MsgHeartbeat)
	}
}

func TestInvalidMagic(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0, 0, 0, 0, Version, 0, 0})
	_, err := ReadHandshake(buf)
	if err == nil {
		t.Error("expected error for invalid magic")
	}
}

func TestServerClientIntegration(t *testing.T) {
	eventCh := make(chan ReplicationEvent, 100)

	srv, err := NewServer("127.0.0.1:0", eventCh)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.Run()
	defer srv.Stop()

	addr := srv.listener.Addr().String()

	// Track received events
	var received []ReplicationEvent
	done := make(chan struct{})

	client := NewClient(addr, []string{"BTC-USD"}, func(inst string, record []byte) {
		received = append(received, ReplicationEvent{Instrument: inst, Record: record})
		if len(received) >= 3 {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Stop()

	time.Sleep(50 * time.Millisecond) // let connection establish

	// Send 3 events
	for i := 0; i < 3; i++ {
		eventCh <- ReplicationEvent{
			Instrument: "BTC-USD",
			Record:     []byte{byte(i), 1, 2, 3},
		}
	}

	// Wait for receipt
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout: received %d events, want 3", len(received))
	}

	if len(received) < 3 {
		t.Errorf("received %d events, want 3", len(received))
	}
}

func TestServerReplicaCount(t *testing.T) {
	eventCh := make(chan ReplicationEvent, 100)
	srv, err := NewServer("127.0.0.1:0", eventCh)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.Run()
	defer srv.Stop()

	if srv.ReplicaCount() != 0 {
		t.Errorf("initial replica count = %d", srv.ReplicaCount())
	}

	addr := srv.listener.Addr().String()
	client := NewClient(addr, []string{"BTC-USD"}, nil)
	client.Connect()
	defer client.Stop()

	time.Sleep(100 * time.Millisecond)
	if srv.ReplicaCount() != 1 {
		t.Errorf("replica count = %d, want 1", srv.ReplicaCount())
	}
}
