package fix

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Session manages a single FIX client connection.
type Session struct {
	conn          net.Conn
	reader        *bufio.Reader
	senderCompID  string
	targetCompID  string
	inSeqNum      uint64
	outSeqNum     uint64
	heartbeatInt  int // seconds
	loggedOn      int32
	lastRecvTime  time.Time
	sendMu        sync.Mutex
	onMessage     func(*Session, *Message) // application message handler
	onLogon       func(*Session)           // called after successful logon
	stopCh        chan struct{}
}

// NewSession creates a FIX session from an accepted connection.
func NewSession(conn net.Conn, targetCompID string, onMessage func(*Session, *Message)) *Session {
	return &Session{
		conn:         conn,
		reader:       bufio.NewReaderSize(conn, 64*1024),
		targetCompID: targetCompID,
		onMessage:    onMessage,
		stopCh:       make(chan struct{}),
	}
}

// SenderCompID returns the client's CompID.
func (s *Session) SenderCompID() string {
	return s.senderCompID
}

// IsLoggedOn returns whether the session is active.
func (s *Session) IsLoggedOn() bool {
	return atomic.LoadInt32(&s.loggedOn) == 1
}

// Run starts the session read loop. Blocks until disconnection.
func (s *Session) Run() {
	defer func() {
		s.conn.Close()
		atomic.StoreInt32(&s.loggedOn, 0)
	}()

	// Start heartbeat goroutine once logged on
	go s.heartbeatLoop()

	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		msg, err := s.readMessage()
		if err != nil {
			return
		}

		s.lastRecvTime = time.Now()

		switch msg.MsgType {
		case MsgTypeLogon:
			s.handleLogon(msg)
		case MsgTypeLogout:
			s.handleLogout(msg)
		case MsgTypeHeartbeat:
			// acknowledged
		case MsgTypeTestRequest:
			s.sendHeartbeat()
		default:
			if !s.IsLoggedOn() {
				s.sendLogout("not logged on")
				return
			}
			if s.onMessage != nil {
				s.onMessage(s, msg)
			}
		}
	}
}

// Stop closes the session.
func (s *Session) Stop() {
	close(s.stopCh)
	s.conn.Close()
}

// Send sends a FIX message to the client.
func (s *Session) Send(msg *Message) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	seq := atomic.AddUint64(&s.outSeqNum, 1)
	msg.SetField(TagMsgSeqNum, fmt.Sprintf("%d", seq))
	msg.SetField(TagSenderCompID, s.targetCompID)
	msg.SetField(TagTargetCompID, s.senderCompID)
	msg.SetField(TagSendingTime, time.Now().UTC().Format("20060102-15:04:05.000"))

	data := Encode(msg)
	_, err := s.conn.Write(data)
	return err
}

func (s *Session) handleLogon(msg *Message) {
	s.senderCompID = msg.GetField(TagSenderCompID)
	if hb, err := msg.GetInt(TagHeartBtInt); err == nil {
		s.heartbeatInt = int(hb)
	} else {
		s.heartbeatInt = 30
	}

	atomic.StoreInt32(&s.loggedOn, 1)
	log.Printf("[fix] logon from %s (heartbeat=%ds)", s.senderCompID, s.heartbeatInt)

	// Send logon response
	resp := NewMessage(MsgTypeLogon)
	resp.SetField(TagHeartBtInt, fmt.Sprintf("%d", s.heartbeatInt))
	s.Send(resp)

	// Notify gateway of successful logon (for session registration)
	if s.onLogon != nil {
		s.onLogon(s)
	}
}

func (s *Session) handleLogout(msg *Message) {
	log.Printf("[fix] logout from %s", s.senderCompID)
	resp := NewMessage(MsgTypeLogout)
	s.Send(resp)
	atomic.StoreInt32(&s.loggedOn, 0)
}

func (s *Session) sendHeartbeat() {
	msg := NewMessage(MsgTypeHeartbeat)
	s.Send(msg)
}

func (s *Session) sendLogout(reason string) {
	msg := NewMessage(MsgTypeLogout)
	if reason != "" {
		msg.SetField(58, reason) // tag 58 = Text
	}
	s.Send(msg)
}

func (s *Session) readMessage() (*Message, error) {
	// Read until we find a complete FIX message (ends with \x0110=NNN\x01).
	// We require exactly 3 ASCII digits between "10=" and the terminating SOH;
	// loose framing here lets a peer truncate the checksum and bypass validation.
	var data []byte
	const maxMessageSize = 1 << 20 // 1MB hard cap to bound memory per session
	for {
		b, err := s.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		data = append(data, b)
		if len(data) > maxMessageSize {
			return nil, fmt.Errorf("FIX message exceeds %d bytes", maxMessageSize)
		}
		if b != SOH || len(data) < 8 {
			continue
		}
		// data[-8..] should be "\x0110=NNN\x01"
		tail := data[len(data)-8:]
		if tail[0] == SOH && tail[1] == '1' && tail[2] == '0' && tail[3] == '=' &&
			isASCIIDigit(tail[4]) && isASCIIDigit(tail[5]) && isASCIIDigit(tail[6]) {
			break
		}
	}

	return Parse(data)
}

func (s *Session) heartbeatLoop() {
	if s.heartbeatInt <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(s.heartbeatInt) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if s.IsLoggedOn() {
				s.sendHeartbeat()
			}
		case <-s.stopCh:
			return
		}
	}
}
