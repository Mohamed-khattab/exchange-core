package ws

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

const (
	sendBufferSize = 256
	pingInterval   = 30 * time.Second
)

// channelSet tracks which channels a client is subscribed to.
type channelSet struct {
	trades bool
	book   bool
	ticker bool
}

func (cs channelSet) has(ch string) bool {
	switch ch {
	case "trades":
		return cs.trades
	case "book":
		return cs.book
	case "ticker":
		return cs.ticker
	}
	return false
}

// Client represents a single WebSocket connection.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.RWMutex
	subs map[string]channelSet // instrument -> channels

	closed  int32
	dropped int64
}

func newClient(hub *Hub, conn *websocket.Conn) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, sendBufferSize),
		ctx:    ctx,
		cancel: cancel,
		subs:   make(map[string]channelSet),
	}
}

// isSubscribed checks if the client wants events of the given type for the instrument.
func (c *Client) isSubscribed(instrument, channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cs, ok := c.subs[instrument]
	if !ok {
		return false
	}
	return cs.has(channel)
}

// readPump reads messages from the client and processes subscription commands.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, msg, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		c.handleMessage(msg)
	}
}

func (c *Client) handleMessage(msg []byte) {
	var cm ClientMessage
	if err := json.Unmarshal(msg, &cm); err != nil {
		c.sendMsg(MarshalServerMessage(&ServerMessage{Type: "error", Error: "invalid JSON"}))
		return
	}

	switch cm.Type {
	case "subscribe":
		c.subscribe(cm.Instruments, cm.Channels)
		c.sendMsg(MarshalServerMessage(&ServerMessage{
			Type: "subscribed", Channels: cm.Channels, Instruments: cm.Instruments,
		}))
	case "unsubscribe":
		c.unsubscribe(cm.Instruments, cm.Channels)
		c.sendMsg(MarshalServerMessage(&ServerMessage{
			Type: "unsubscribed", Channels: cm.Channels, Instruments: cm.Instruments,
		}))
	case "ping":
		c.sendMsg(MarshalServerMessage(&ServerMessage{Type: "pong"}))
	default:
		c.sendMsg(MarshalServerMessage(&ServerMessage{Type: "error", Error: "unknown message type"}))
	}
}

func (c *Client) subscribe(instruments, channels []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, inst := range instruments {
		cs := c.subs[inst]
		for _, ch := range channels {
			switch ch {
			case "trades":
				cs.trades = true
			case "book":
				cs.book = true
			case "ticker":
				cs.ticker = true
			}
		}
		c.subs[inst] = cs
	}
}

func (c *Client) unsubscribe(instruments, channels []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, inst := range instruments {
		cs := c.subs[inst]
		for _, ch := range channels {
			switch ch {
			case "trades":
				cs.trades = false
			case "book":
				cs.book = false
			case "ticker":
				cs.ticker = false
			}
		}
		// Clean up empty subscriptions
		if !cs.trades && !cs.book && !cs.ticker {
			delete(c.subs, inst)
		} else {
			c.subs[inst] = cs
		}
	}
}

// writePump drains the send channel and writes to the WebSocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return // channel closed
			}
			if err := c.conn.Write(c.ctx, websocket.MessageText, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.Ping(c.ctx); err != nil {
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) sendMsg(msg []byte) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return
	}
	select {
	case c.send <- msg:
	default:
		atomic.AddInt64(&c.dropped, 1)
		log.Printf("[ws] dropped message for slow client")
	}
}

func (c *Client) close() {
	if atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		c.cancel()
		close(c.send)
	}
}
