package fix

import (
	"fmt"
	"strconv"
	"strings"
)

const SOH = '\x01' // FIX field delimiter

// Message represents a parsed FIX message.
type Message struct {
	MsgType string
	Fields  map[int]string
	Raw     []byte
}

// NewMessage creates an empty FIX message with the given type.
func NewMessage(msgType string) *Message {
	return &Message{
		MsgType: msgType,
		Fields:  make(map[int]string),
	}
}

// GetField returns the value of a tag, or empty string if not present.
func (m *Message) GetField(tag int) string {
	return m.Fields[tag]
}

// SetField sets a tag value.
func (m *Message) SetField(tag int, value string) {
	m.Fields[tag] = value
}

// GetInt returns an integer field value.
func (m *Message) GetInt(tag int) (int64, error) {
	v, ok := m.Fields[tag]
	if !ok {
		return 0, fmt.Errorf("tag %d not found", tag)
	}
	return strconv.ParseInt(v, 10, 64)
}

// GetFloat returns a float field value.
func (m *Message) GetFloat(tag int) (float64, error) {
	v, ok := m.Fields[tag]
	if !ok {
		return 0, fmt.Errorf("tag %d not found", tag)
	}
	return strconv.ParseFloat(v, 64)
}

// Parse parses a FIX message from raw bytes.
// Format: 8=FIX.4.4\x019=<len>\x0135=<type>\x01...10=<checksum>\x01
func Parse(data []byte) (*Message, error) {
	msg := &Message{
		Fields: make(map[int]string),
		Raw:    data,
	}

	// Locate the checksum trailer "\x0110=NNN\x01" (exactly 3 ASCII digits).
	// The checksum field must be the last field of the message.
	if len(data) < 8 || data[len(data)-1] != SOH {
		return nil, fmt.Errorf("malformed FIX message: missing terminating SOH")
	}
	csStart := len(data) - 7 // index of '1' in "10=NNN\x01"
	if csStart < 0 || data[csStart-1] != SOH ||
		data[csStart] != '1' || data[csStart+1] != '0' || data[csStart+2] != '=' ||
		!isASCIIDigit(data[csStart+3]) || !isASCIIDigit(data[csStart+4]) || !isASCIIDigit(data[csStart+5]) {
		return nil, fmt.Errorf("malformed FIX message: invalid checksum field")
	}

	// Verify checksum: sum of all bytes before the "10=" field (including the SOH
	// that delimits it), mod 256. The trailer itself is not in the sum.
	sum := 0
	for _, b := range data[:csStart] {
		sum += int(b)
	}
	expected := sum % 256
	declared, err := strconv.Atoi(string(data[csStart+3 : csStart+6]))
	if err != nil {
		return nil, fmt.Errorf("malformed FIX message: non-numeric checksum")
	}
	if declared != expected {
		return nil, fmt.Errorf("FIX checksum mismatch: declared=%d computed=%d", declared, expected)
	}

	s := string(data)
	pairs := strings.Split(s, string(SOH))

	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		eqIdx := strings.IndexByte(pair, '=')
		if eqIdx < 0 {
			continue
		}
		tagStr := pair[:eqIdx]
		value := pair[eqIdx+1:]
		tag, err := strconv.Atoi(tagStr)
		if err != nil {
			continue
		}
		msg.Fields[tag] = value
	}

	msg.MsgType = msg.Fields[TagMsgType]
	if msg.MsgType == "" {
		return nil, fmt.Errorf("missing MsgType (tag 35)")
	}

	// Verify begin string
	if msg.Fields[TagBeginString] != BeginString {
		return nil, fmt.Errorf("invalid BeginString: %s", msg.Fields[TagBeginString])
	}

	return msg, nil
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// Encode serializes a FIX message to wire format.
func Encode(msg *Message) []byte {
	// Build body (all fields except 8, 9, 10)
	var body strings.Builder

	// Tag 35 must come first in the body
	body.WriteString(fmt.Sprintf("%d=%s%c", TagMsgType, msg.MsgType, SOH))

	for tag, value := range msg.Fields {
		if tag == TagBeginString || tag == TagBodyLength || tag == TagCheckSum || tag == TagMsgType {
			continue
		}
		body.WriteString(fmt.Sprintf("%d=%s%c", tag, value, SOH))
	}

	bodyStr := body.String()
	bodyLen := len(bodyStr)

	// Build full message: 8=...\x01 9=<len>\x01 <body> 10=<checksum>\x01
	var full strings.Builder
	full.WriteString(fmt.Sprintf("%d=%s%c", TagBeginString, BeginString, SOH))
	full.WriteString(fmt.Sprintf("%d=%d%c", TagBodyLength, bodyLen, SOH))
	full.WriteString(bodyStr)

	// Calculate checksum (sum of all bytes before checksum field, mod 256)
	sum := 0
	for _, b := range []byte(full.String()) {
		sum += int(b)
	}
	full.WriteString(fmt.Sprintf("%d=%03d%c", TagCheckSum, sum%256, SOH))

	return []byte(full.String())
}
