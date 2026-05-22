package replication

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"
)

// Client connects to a primary replication server and receives WAL events.
type Client struct {
	primaryAddr string
	instruments []string
	conn        net.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	onEvent     func(instrument string, record []byte) // callback for each received event
	authSecret  []byte
}

// NewClient creates a replication client. authSecret is required and must be
// at least 16 bytes; it must match the primary's secret.
func NewClient(primaryAddr string, instruments []string, authSecret []byte, onEvent func(string, []byte)) (*Client, error) {
	if len(authSecret) < 16 {
		return nil, fmt.Errorf("replication: authSecret must be at least 16 bytes")
	}
	secret := make([]byte, len(authSecret))
	copy(secret, authSecret)
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		primaryAddr: primaryAddr,
		instruments: instruments,
		ctx:         ctx,
		cancel:      cancel,
		onEvent:     onEvent,
		authSecret:  secret,
	}, nil
}

// Connect establishes a connection to the primary and starts receiving events.
func (c *Client) Connect() error {
	conn, err := net.Dial("tcp", c.primaryAddr)
	if err != nil {
		return fmt.Errorf("connecting to primary: %w", err)
	}
	c.conn = conn

	// Bound handshake time so a misconfigured primary can't hang us forever.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	// Send handshake
	if err := WriteHandshake(conn, c.instruments); err != nil {
		conn.Close()
		return fmt.Errorf("writing handshake: %w", err)
	}

	// Read challenge and answer it.
	nonce, err := ReadChallenge(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("reading challenge: %w", err)
	}
	tag := computeAuthTag(c.authSecret, nonce[:], c.instruments)
	if err := WriteAuthResponse(conn, tag); err != nil {
		conn.Close()
		return fmt.Errorf("writing auth response: %w", err)
	}

	// Read response
	accepted, err := ReadResponse(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("reading response: %w", err)
	}
	if !accepted {
		conn.Close()
		return fmt.Errorf("connection rejected by primary")
	}

	_ = conn.SetDeadline(time.Time{})
	log.Printf("[replication] connected to primary at %s", c.primaryAddr)

	// Start receiving events
	go c.receiveLoop()
	return nil
}

// Stop disconnects from the primary.
func (c *Client) Stop() {
	c.cancel()
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *Client) receiveLoop() {
	defer func() {
		if c.conn != nil {
			c.conn.Close()
		}
		log.Printf("[replication] disconnected from primary")
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		msgType, instrument, data, err := ReadMessage(c.conn)
		if err != nil {
			select {
			case <-c.ctx.Done():
				return
			default:
				log.Printf("[replication] read error: %v", err)
				return
			}
		}

		switch msgType {
		case MsgWALEvent:
			if c.onEvent != nil {
				c.onEvent(instrument, data)
			}
		case MsgHeartbeat:
			// ignore
		}
	}
}
