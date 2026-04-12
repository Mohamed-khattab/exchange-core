package replication

import (
	"context"
	"log"
	"net"
	"sync"
)

// Server streams WAL events to connected replicas.
type Server struct {
	listener net.Listener
	replicas map[*replicaConn]struct{}
	mu       sync.RWMutex
	eventCh  <-chan ReplicationEvent
	ctx      context.Context
	cancel   context.CancelFunc
}

type replicaConn struct {
	conn   net.Conn
	sendCh chan []byte
}

// NewServer creates a replication server that reads events from the channel
// and fans them out to connected replicas.
func NewServer(addr string, eventCh <-chan ReplicationEvent) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		listener: listener,
		replicas: make(map[*replicaConn]struct{}),
		eventCh:  eventCh,
		ctx:      ctx,
		cancel:   cancel,
	}
	return s, nil
}

// Run starts accepting replica connections and streaming events.
func (s *Server) Run() {
	go s.acceptLoop()
	go s.broadcastLoop()
	log.Printf("[replication] server listening on %s", s.listener.Addr())
}

// Stop shuts down the replication server.
func (s *Server) Stop() {
	s.cancel()
	s.listener.Close()
	s.mu.Lock()
	for rc := range s.replicas {
		rc.conn.Close()
		close(rc.sendCh)
		delete(s.replicas, rc)
	}
	s.mu.Unlock()
}

// ReplicaCount returns the number of connected replicas.
func (s *Server) ReplicaCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.replicas)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				log.Printf("[replication] accept error: %v", err)
				continue
			}
		}
		go s.handleReplica(conn)
	}
}

func (s *Server) handleReplica(conn net.Conn) {
	// Read handshake
	instruments, err := ReadHandshake(conn)
	if err != nil {
		log.Printf("[replication] handshake error: %v", err)
		conn.Close()
		return
	}
	log.Printf("[replication] replica connected for instruments: %v", instruments)

	if err := WriteResponse(conn, true); err != nil {
		conn.Close()
		return
	}

	rc := &replicaConn{
		conn:   conn,
		sendCh: make(chan []byte, 10_000),
	}

	s.mu.Lock()
	s.replicas[rc] = struct{}{}
	s.mu.Unlock()

	// Writer goroutine
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.replicas, rc)
			s.mu.Unlock()
			conn.Close()
			log.Printf("[replication] replica disconnected")
		}()

		for data := range rc.sendCh {
			if _, err := conn.Write(data); err != nil {
				return
			}
		}
	}()
}

func (s *Server) broadcastLoop() {
	for {
		select {
		case event, ok := <-s.eventCh:
			if !ok {
				return
			}
			s.mu.RLock()
			for rc := range s.replicas {
				// Non-blocking send
				select {
				case rc.sendCh <- encodeEvent(event):
				default:
					// Replica lagging, will catch up from WAL files
				}
			}
			s.mu.RUnlock()

		case <-s.ctx.Done():
			return
		}
	}
}

func encodeEvent(event ReplicationEvent) []byte {
	instLen := len(event.Instrument)
	buf := make([]byte, 1+2+instLen+4+len(event.Record))
	buf[0] = MsgWALEvent
	buf[1] = byte(instLen >> 8)
	buf[2] = byte(instLen)
	copy(buf[3:], event.Instrument)
	off := 3 + instLen
	buf[off] = byte(len(event.Record) >> 24)
	buf[off+1] = byte(len(event.Record) >> 16)
	buf[off+2] = byte(len(event.Record) >> 8)
	buf[off+3] = byte(len(event.Record))
	copy(buf[off+4:], event.Record)
	return buf
}
