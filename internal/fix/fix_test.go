package fix

import (
	"fmt"
	"strings"
	"testing"

	"github.com/trading/matching-engine/internal/models"
)

// withChecksum appends a correct "10=NNN\x01" trailer to body bytes.
// body must already end with SOH.
func withChecksum(body string) []byte {
	sum := 0
	for _, b := range []byte(body) {
		sum += int(b)
	}
	return []byte(body + fmt.Sprintf("10=%03d%c", sum%256, SOH))
}

func TestParseMessage(t *testing.T) {
	body := fmt.Sprintf("8=FIX.4.4%c9=100%c35=D%c49=CLIENT%c56=SERVER%c11=ord-001%c55=BTC-USD%c54=1%c40=2%c44=50000%c38=1.5%c",
		SOH, SOH, SOH, SOH, SOH, SOH, SOH, SOH, SOH, SOH, SOH)
	raw := withChecksum(body)

	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if msg.MsgType != MsgTypeNewOrderSingle {
		t.Errorf("MsgType = %s, want D", msg.MsgType)
	}
	if msg.GetField(TagClOrdID) != "ord-001" {
		t.Errorf("ClOrdID = %s", msg.GetField(TagClOrdID))
	}
	if msg.GetField(TagSymbol) != "BTC-USD" {
		t.Errorf("Symbol = %s", msg.GetField(TagSymbol))
	}
	if msg.GetField(TagSide) != SideBuy {
		t.Errorf("Side = %s", msg.GetField(TagSide))
	}

	price, err := msg.GetFloat(TagPrice)
	if err != nil {
		t.Fatalf("GetFloat(Price): %v", err)
	}
	if price != 50000 {
		t.Errorf("Price = %f", price)
	}
}

func TestParseInvalidBeginString(t *testing.T) {
	raw := withChecksum(fmt.Sprintf("8=FIX.4.2%c35=D%c", SOH, SOH))
	_, err := Parse(raw)
	if err == nil {
		t.Error("expected error for invalid BeginString")
	}
}

func TestParseMissingMsgType(t *testing.T) {
	raw := withChecksum(fmt.Sprintf("8=FIX.4.4%c9=10%c", SOH, SOH))
	_, err := Parse(raw)
	if err == nil {
		t.Error("expected error for missing MsgType")
	}
}

func TestParseRejectsChecksumMismatch(t *testing.T) {
	body := fmt.Sprintf("8=FIX.4.4%c9=10%c35=0%c", SOH, SOH, SOH)
	// Append a deliberately wrong checksum.
	bad := []byte(body + fmt.Sprintf("10=001%c", SOH))
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected error for checksum mismatch")
	}
}

func TestParseRejectsMalformedChecksumField(t *testing.T) {
	cases := map[string]string{
		"non-digit checksum":   fmt.Sprintf("8=FIX.4.4%c35=0%c10=ABC%c", SOH, SOH, SOH),
		"too-short checksum":   fmt.Sprintf("8=FIX.4.4%c35=0%c10=1%c", SOH, SOH, SOH),
		"missing terminator":   fmt.Sprintf("8=FIX.4.4%c35=0%c10=000", SOH, SOH),
		"no checksum at all":   fmt.Sprintf("8=FIX.4.4%c35=0%c", SOH, SOH),
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestEncodeMessage(t *testing.T) {
	msg := NewMessage(MsgTypeLogon)
	msg.SetField(TagSenderCompID, "SERVER")
	msg.SetField(TagTargetCompID, "CLIENT")
	msg.SetField(TagHeartBtInt, "30")

	encoded := Encode(msg)
	s := string(encoded)

	if !strings.HasPrefix(s, "8=FIX.4.4\x01") {
		t.Errorf("missing BeginString prefix: %s", s)
	}
	if !strings.Contains(s, "35=A\x01") {
		t.Errorf("missing MsgType: %s", s)
	}
	if !strings.Contains(s, "10=") {
		t.Errorf("missing checksum: %s", s)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	msg := NewMessage(MsgTypeNewOrderSingle)
	msg.SetField(TagClOrdID, "test-123")
	msg.SetField(TagSymbol, "ETH-USD")
	msg.SetField(TagSide, SideSell)
	msg.SetField(TagOrdType, OrdTypeLimit)
	msg.SetField(TagPrice, "3000.50")
	msg.SetField(TagOrderQty, "10")

	encoded := Encode(msg)
	decoded, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if decoded.MsgType != MsgTypeNewOrderSingle {
		t.Errorf("MsgType = %s", decoded.MsgType)
	}
	if decoded.GetField(TagClOrdID) != "test-123" {
		t.Errorf("ClOrdID = %s", decoded.GetField(TagClOrdID))
	}
	if decoded.GetField(TagSymbol) != "ETH-USD" {
		t.Errorf("Symbol = %s", decoded.GetField(TagSymbol))
	}
}

func TestNewMessage(t *testing.T) {
	msg := NewMessage(MsgTypeHeartbeat)
	if msg.MsgType != MsgTypeHeartbeat {
		t.Errorf("MsgType = %s", msg.MsgType)
	}
	msg.SetField(100, "value")
	if msg.GetField(100) != "value" {
		t.Errorf("GetField(100) = %s", msg.GetField(100))
	}
}

func TestGetIntMissing(t *testing.T) {
	msg := NewMessage(MsgTypeLogon)
	_, err := msg.GetInt(999)
	if err == nil {
		t.Error("expected error for missing tag")
	}
}

func TestGetFloatMissing(t *testing.T) {
	msg := NewMessage(MsgTypeLogon)
	_, err := msg.GetFloat(999)
	if err == nil {
		t.Error("expected error for missing tag")
	}
}

func TestFixSideMapping(t *testing.T) {
	if fixSideToEngine(SideBuy) != "BUY" {
		t.Error("SideBuy mapping")
	}
	if fixSideToEngine(SideSell) != "SELL" {
		t.Error("SideSell mapping")
	}
}

func TestFixOrdTypeMapping(t *testing.T) {
	if fixOrdTypeToEngine(OrdTypeMarket) != "MARKET" {
		t.Error("OrdType market mapping")
	}
	if fixOrdTypeToEngine(OrdTypeLimit) != "LIMIT" {
		t.Error("OrdType limit mapping")
	}
}

func TestEngineStatusToFix(t *testing.T) {
	cases := map[models.OrderStatus]string{
		models.StatusNew:             OrdStatusNew,
		models.StatusPartiallyFilled: OrdStatusPartiallyFilled,
		models.StatusFilled:          OrdStatusFilled,
		models.StatusCancelled:       OrdStatusCancelled,
		models.StatusRejected:        OrdStatusRejected,
	}
	for eng, fix := range cases {
		result := engineStatusToFix(eng)
		if result != fix {
			t.Errorf("%s -> %s, want %s", eng, result, fix)
		}
	}
}
