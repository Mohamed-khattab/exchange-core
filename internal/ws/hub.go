package ws

import (
	"context"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

// Hub manages all WebSocket clients and event dispatch.
type Hub struct {
	mu          sync.RWMutex
	clients     map[*Client]struct{}
	register    chan *Client
	unregister  chan *Client
	broadcast   chan *Event
	maxClients  int
	droppedCnt  uint64 // total events dropped due to full broadcast channel
	lastDropLog atomic.Int64
}

// NewHub creates a new WebSocket hub.
func NewHub(maxClients int) *Hub {
	if maxClients <= 0 {
		maxClients = 1000
	}
	return &Hub{
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		broadcast:  make(chan *Event, 10_000),
		maxClients: maxClients,
	}
}

// Run starts the hub event loop. Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.mu.Unlock()
			log.Printf("[ws] client connected (%d total)", h.ClientCount())

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.close()
			}
			h.mu.Unlock()
			log.Printf("[ws] client disconnected (%d remaining)", h.ClientCount())

		case event := <-h.broadcast:
			msg := MarshalEvent(event)
			h.mu.RLock()
			for client := range h.clients {
				if client.isSubscribed(event.Instrument, event.Type) {
					client.sendMsg(msg)
				}
			}
			h.mu.RUnlock()

		case <-ctx.Done():
			h.mu.Lock()
			for client := range h.clients {
				client.close()
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
		}
	}
}

// Publish sends an event to all subscribed clients (non-blocking).
// When the broadcast channel is saturated the event is dropped; the running
// total is exposed via DroppedCount() and a rate-limited WARN log surfaces the
// condition so a saturated feed is never silent.
func (h *Hub) Publish(event *Event) {
	select {
	case h.broadcast <- event:
	default:
		total := atomic.AddUint64(&h.droppedCnt, 1)
		// Rate-limit the log to once per second to avoid flooding under saturation.
		now := time.Now().UnixNano()
		last := h.lastDropLog.Load()
		if now-last > int64(time.Second) && h.lastDropLog.CompareAndSwap(last, now) {
			log.Printf("[ws] broadcast channel full; dropped event type=%s instrument=%s (total dropped=%d)",
				event.Type, event.Instrument, total)
		}
	}
}

// DroppedCount returns the total number of events dropped because the
// broadcast channel was full. Used by Prometheus exporters and tests.
func (h *Hub) DroppedCount() uint64 {
	return atomic.LoadUint64(&h.droppedCnt)
}

// HandleUpgrade upgrades an HTTP request to a WebSocket connection.
func (h *Hub) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	if h.ClientCount() >= h.maxClients {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	client := newClient(h, conn)
	h.register <- client

	go client.writePump()
	go client.readPump()
}

// ClientCount returns the current number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
