// Package audit provides compliance audit trail reading and export.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/wal"
)

// AuditRecord is a structured representation of a WAL event for compliance.
type AuditRecord struct {
	SeqNo      uint64      `json:"seq_no"`
	EventType  string      `json:"event_type"`
	Timestamp  string      `json:"timestamp"` // RFC3339Nano
	Instrument string      `json:"instrument,omitempty"`
	ClientID   string      `json:"client_id,omitempty"`
	OrderID    uint64      `json:"order_id,omitempty"`
	TradeID    uint64      `json:"trade_id,omitempty"`
	Details    interface{} `json:"details,omitempty"`
	ReasonCode uint16      `json:"reason_code,omitempty"`
	ReasonText string      `json:"reason_text,omitempty"`
}

var eventTypeNames = map[uint8]string{
	wal.EventOrderAdd:       "ORDER_ADD",
	wal.EventOrderCancel:    "ORDER_CANCEL",
	wal.EventStopActivation: "STOP_ACTIVATION",
	wal.EventOrderAmend:     "ORDER_AMEND",
	wal.EventMassCancel:     "MASS_CANCEL",
	wal.EventOrderRejected:  "ORDER_REJECTED",
	wal.EventTradeExecuted:  "TRADE_EXECUTED",
}

// ExportJSON writes audit records as newline-delimited JSON (NDJSON).
func ExportJSON(walDir, instrument string, fromSeq, toSeq uint64, w io.Writer) (int, error) {
	reader := wal.NewReader(walDir, instrument)
	enc := json.NewEncoder(w)
	count := 0

	_, err := reader.Replay(fromSeq, func(seqNo uint64, eventType uint8, payload []byte) error {
		if toSeq > 0 && seqNo > toSeq {
			return nil
		}

		record, err := decodeToAuditRecord(seqNo, eventType, payload)
		if err != nil {
			return nil // skip malformed records
		}

		if err := enc.Encode(record); err != nil {
			return err
		}
		count++
		return nil
	})

	return count, err
}

func decodeToAuditRecord(seqNo uint64, eventType uint8, payload []byte) (*AuditRecord, error) {
	record := &AuditRecord{
		SeqNo:     seqNo,
		EventType: eventTypeNames[eventType],
		Timestamp: time.Now().Format(time.RFC3339Nano), // fallback
	}

	switch eventType {
	case wal.EventOrderAdd, wal.EventStopActivation:
		order, err := wal.DecodeOrderAdd(payload)
		if err != nil {
			return nil, err
		}
		record.Instrument = order.Instrument
		record.ClientID = order.ClientID
		record.OrderID = order.ID
		record.Timestamp = order.CreatedAt.Format(time.RFC3339Nano)
		record.Details = map[string]interface{}{
			"side":     order.Side.String(),
			"type":     string(order.Type),
			"price":    models.PriceToFloat(order.Price),
			"quantity": models.QtyToFloat(order.Quantity),
		}

	case wal.EventOrderCancel:
		orderID, instrument, err := wal.DecodeOrderCancel(payload)
		if err != nil {
			return nil, err
		}
		record.OrderID = orderID
		record.Instrument = instrument

	case wal.EventOrderAmend:
		orderID, instrument, newPrice, newQty, err := wal.DecodeOrderAmend(payload)
		if err != nil {
			return nil, err
		}
		record.OrderID = orderID
		record.Instrument = instrument
		record.Details = map[string]interface{}{
			"new_price":    models.PriceToFloat(newPrice),
			"new_quantity": models.QtyToFloat(newQty),
		}

	case wal.EventOrderRejected:
		orderID, clientID, instrument, reasonCode, reasonText, err := wal.DecodeOrderRejected(payload)
		if err != nil {
			return nil, err
		}
		record.OrderID = orderID
		record.ClientID = clientID
		record.Instrument = instrument
		record.ReasonCode = reasonCode
		record.ReasonText = reasonText

	case wal.EventTradeExecuted:
		tradeID, instrument, buyOrderID, sellOrderID, buyClientID, sellClientID, price, qty, _, tsNano, err := wal.DecodeTradeExecuted(payload)
		if err != nil {
			return nil, err
		}
		record.TradeID = tradeID
		record.Instrument = instrument
		record.Timestamp = time.Unix(0, tsNano).UTC().Format(time.RFC3339Nano)
		record.Details = map[string]interface{}{
			"buy_order_id":   buyOrderID,
			"sell_order_id":  sellOrderID,
			"buy_client_id":  buyClientID,
			"sell_client_id": sellClientID,
			"price":          models.PriceToFloat(price),
			"quantity":       models.QtyToFloat(qty),
		}

	case wal.EventMassCancel:
		instrument, clientID, _, err := wal.DecodeMassCancel(payload)
		if err != nil {
			return nil, err
		}
		record.Instrument = instrument
		record.ClientID = clientID

	default:
		record.EventType = fmt.Sprintf("UNKNOWN_%d", eventType)
	}

	return record, nil
}
