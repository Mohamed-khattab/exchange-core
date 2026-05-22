// Package replication implements primary-replica WAL event streaming
// for hot standby failover.
package replication

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

// nonceLen is the size of the per-connection challenge nonce.
const nonceLen = 16

// hmacLen is the size of an HMAC-SHA256 tag.
const hmacLen = sha256.Size

// Protocol constants
var Magic = [4]byte{'R', 'E', 'P', 'L'}

const Version uint8 = 1

// Message types
const (
	MsgSnapshot  uint8 = 1
	MsgWALEvent  uint8 = 2
	MsgACK       uint8 = 3
	MsgHeartbeat uint8 = 4
)

// ReplicationEvent is a WAL event tagged with its instrument.
type ReplicationEvent struct {
	Instrument string
	Record     []byte // raw WAL record bytes
}

// WriteHandshake writes the replication handshake to a writer.
func WriteHandshake(w io.Writer, instruments []string) error {
	if _, err := w.Write(Magic[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{Version}); err != nil {
		return err
	}
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], uint16(len(instruments)))
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	for _, inst := range instruments {
		binary.BigEndian.PutUint16(buf[:], uint16(len(inst)))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
		if _, err := w.Write([]byte(inst)); err != nil {
			return err
		}
	}
	return nil
}

// ReadHandshake reads and validates the replication handshake.
func ReadHandshake(r io.Reader) ([]string, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if magic != Magic {
		return nil, fmt.Errorf("invalid magic: %v", magic)
	}
	var ver [1]byte
	if _, err := io.ReadFull(r, ver[:]); err != nil {
		return nil, err
	}
	if ver[0] != Version {
		return nil, fmt.Errorf("unsupported version: %d", ver[0])
	}
	var countBuf [2]byte
	if _, err := io.ReadFull(r, countBuf[:]); err != nil {
		return nil, err
	}
	count := int(binary.BigEndian.Uint16(countBuf[:]))
	instruments := make([]string, count)
	for i := 0; i < count; i++ {
		if _, err := io.ReadFull(r, countBuf[:]); err != nil {
			return nil, err
		}
		l := int(binary.BigEndian.Uint16(countBuf[:]))
		name := make([]byte, l)
		if _, err := io.ReadFull(r, name); err != nil {
			return nil, err
		}
		instruments[i] = string(name)
	}
	return instruments, nil
}

// WriteResponse writes a handshake response (OK=0, REJECTED=1).
func WriteResponse(w io.Writer, accepted bool) error {
	if _, err := w.Write(Magic[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{Version}); err != nil {
		return err
	}
	status := byte(0)
	if !accepted {
		status = 1
	}
	_, err := w.Write([]byte{status})
	return err
}

// ReadResponse reads the handshake response. Returns true if accepted.
func ReadResponse(r io.Reader) (bool, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return false, err
	}
	if magic != Magic {
		return false, fmt.Errorf("invalid response magic")
	}
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return false, err
	}
	if _, err := io.ReadFull(r, buf[1:2]); err != nil {
		return false, err
	}
	return buf[1] == 0, nil
}

// WriteEvent writes a WAL event message.
func WriteEvent(w io.Writer, instrument string, record []byte) error {
	// [MsgType:1][instLen:2][inst:N][recordLen:4][record:N]
	header := make([]byte, 1+2+len(instrument)+4)
	header[0] = MsgWALEvent
	binary.BigEndian.PutUint16(header[1:], uint16(len(instrument)))
	copy(header[3:], instrument)
	binary.BigEndian.PutUint32(header[3+len(instrument):], uint32(len(record)))

	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(record)
	return err
}

// ReadMessage reads a replication message. Returns msgType, instrument, and data.
func ReadMessage(r io.Reader) (msgType uint8, instrument string, data []byte, err error) {
	var typeBuf [1]byte
	if _, err := io.ReadFull(r, typeBuf[:]); err != nil {
		return 0, "", nil, err
	}
	msgType = typeBuf[0]

	switch msgType {
	case MsgWALEvent:
		var instLen [2]byte
		if _, err := io.ReadFull(r, instLen[:]); err != nil {
			return 0, "", nil, err
		}
		l := int(binary.BigEndian.Uint16(instLen[:]))
		instBuf := make([]byte, l)
		if _, err := io.ReadFull(r, instBuf); err != nil {
			return 0, "", nil, err
		}
		instrument = string(instBuf)

		var recLen [4]byte
		if _, err := io.ReadFull(r, recLen[:]); err != nil {
			return 0, "", nil, err
		}
		rl := int(binary.BigEndian.Uint32(recLen[:]))
		data = make([]byte, rl)
		if _, err := io.ReadFull(r, data); err != nil {
			return 0, "", nil, err
		}
		return msgType, instrument, data, nil

	case MsgHeartbeat:
		return msgType, "", nil, nil

	default:
		return msgType, "", nil, fmt.Errorf("unknown message type: %d", msgType)
	}
}

// WriteHeartbeat writes a heartbeat message.
func WriteHeartbeat(w io.Writer) error {
	_, err := w.Write([]byte{MsgHeartbeat})
	return err
}

// newNonce returns a cryptographically random nonce for challenge-response auth.
func newNonce() ([nonceLen]byte, error) {
	var n [nonceLen]byte
	if _, err := io.ReadFull(rand.Reader, n[:]); err != nil {
		return n, fmt.Errorf("generating nonce: %w", err)
	}
	return n, nil
}

// computeAuthTag returns HMAC-SHA256(secret, nonce || instruments-on-the-wire).
// Binding the instruments list defends against an attacker swapping subscriptions
// after the handshake has been signed.
func computeAuthTag(secret, nonce []byte, instruments []string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(nonce)
	for _, inst := range instruments {
		var lb [2]byte
		binary.BigEndian.PutUint16(lb[:], uint16(len(inst)))
		m.Write(lb[:])
		m.Write([]byte(inst))
	}
	return m.Sum(nil)
}

// WriteChallenge sends a server -> client challenge nonce.
func WriteChallenge(w io.Writer, nonce []byte) error {
	if len(nonce) != nonceLen {
		return fmt.Errorf("challenge nonce must be %d bytes", nonceLen)
	}
	_, err := w.Write(nonce)
	return err
}

// ReadChallenge reads a server -> client challenge nonce.
func ReadChallenge(r io.Reader) ([nonceLen]byte, error) {
	var n [nonceLen]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return n, fmt.Errorf("reading challenge: %w", err)
	}
	return n, nil
}

// WriteAuthResponse sends the HMAC-SHA256 tag computed by the client.
func WriteAuthResponse(w io.Writer, tag []byte) error {
	if len(tag) != hmacLen {
		return fmt.Errorf("auth tag must be %d bytes", hmacLen)
	}
	_, err := w.Write(tag)
	return err
}

// ReadAuthResponse reads the HMAC-SHA256 tag sent by the client.
func ReadAuthResponse(r io.Reader) ([]byte, error) {
	tag := make([]byte, hmacLen)
	if _, err := io.ReadFull(r, tag); err != nil {
		return nil, fmt.Errorf("reading auth response: %w", err)
	}
	return tag, nil
}

// verifyAuth performs a constant-time comparison of computed vs received tag.
func verifyAuth(secret, nonce []byte, instruments []string, received []byte) bool {
	expected := computeAuthTag(secret, nonce, instruments)
	return hmac.Equal(expected, received)
}
