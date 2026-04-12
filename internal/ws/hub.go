package ws

import (
	"context"
	"log"
	"net/http"
	"sync"

	"nhooyr.io/websocket"
)

// Hub manages all WebSocket clients and event dispatch.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Event
	maxClients int
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
func (h *Hub) Publish(event *Event) {
	select {
	case h.broadcast <- event:
	default:
		// broadcast buffer full, drop event
	}
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
