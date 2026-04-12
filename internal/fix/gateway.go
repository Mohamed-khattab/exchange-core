package fix

import (
	"context"
	"log"
	"net"
	"sync"

	"github.com/trading/matching-engine/internal/engine"
)

// Gateway is a FIX protocol TCP server that translates FIX messages
// to matching engine API calls.
type Gateway struct {
	listener     net.Listener
	engine       *engine.MatchingEngine
	targetCompID string
	sessions     map[string]*Session
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewGateway creates a FIX gateway.
func NewGateway(addr string, eng *engine.MatchingEngine, targetCompID string) (*Gateway, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{
		listener:     listener,
		engine:       eng,
		targetCompID: targetCompID,
		sessions:     make(map[string]*Session),
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

// Run starts accepting FIX connections. Non-blocking.
func (g *Gateway) Run() {
	go g.acceptLoop()
	log.Printf("[fix] gateway listening on %s (CompID: %s)", g.listener.Addr(), g.targetCompID)
}

// Stop shuts down the gateway.
func (g *Gateway) Stop() {
	g.cancel()
	g.listener.Close()
	g.mu.Lock()
	for _, s := range g.sessions {
		s.Stop()
	}
	g.mu.Unlock()
}

// SessionCount returns the number of active FIX sessions.
func (g *Gateway) SessionCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.sessions)
}

// Addr returns the listener address.
func (g *Gateway) Addr() string {
	return g.listener.Addr().String()
}

func (g *Gateway) acceptLoop() {
	for {
		conn, err := g.listener.Accept()
		if err != nil {
			select {
			case <-g.ctx.Done():
				return
			default:
				log.Printf("[fix] accept error: %v", err)
				continue
			}
		}
		go g.handleConnection(conn)
	}
}

func (g *Gateway) handleConnection(conn net.Conn) {
	handler := NewOrderHandler(g.engine)
	session := NewSession(conn, g.targetCompID, handler.HandleMessage)

	// Set a logon callback so we register the session as soon as the client logs on
	session.onLogon = func(s *Session) {
		g.mu.Lock()
		g.sessions[s.senderCompID] = s
		g.mu.Unlock()
	}

	session.Run() // blocks until disconnection

	// Clean up
	if session.senderCompID != "" {
		g.mu.Lock()
		delete(g.sessions, session.senderCompID)
		g.mu.Unlock()
	}
}
