package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// Record layout (little-endian):
//   [Length:4][CRC32:4][SeqNo:8][Type:1][Payload:variable]
//
// Length = total record size including the 4-byte length field itself.
// CRC32 = IEEE CRC32 over (SeqNo + Type + Payload).

const headerSize = 4 + 4 + 8 + 1 // length(4) + crc(4) + seqno(8) + type(1) = 17

var crcTable = crc32.MakeTable(crc32.IEEE)

// EncodeOrderAdd encodes an order-add event into buf.
// Returns the number of bytes written.
// The caller must ensure buf is large enough (256 bytes is sufficient).
func EncodeOrderAdd(buf []byte, seqNo uint64, order *models.Order) int {
	// Encode payload first (after header)
	off := headerSize

	// Order fields
	binary.LittleEndian.PutUint64(buf[off:], order.ID)
	off += 8

	off += putString(buf[off:], order.ClientID)
	off += putString(buf[off:], order.Instrument)

	buf[off] = byte(order.Side)
	off++

	off += putString(buf[off:], string(order.Type))

	binary.LittleEndian.PutUint64(buf[off:], uint64(order.Price))
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], uint64(order.StopPrice))
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], order.Quantity)
	off += 8

	off += putString(buf[off:], order.TimeInForce)

	binary.LittleEndian.PutUint64(buf[off:], uint64(order.CreatedAt.UnixNano()))
	off += 8

	// Now write the header
	totalLen := off
	binary.LittleEndian.PutUint32(buf[0:], uint32(totalLen))

	// SeqNo and Type
	binary.LittleEndian.PutUint64(buf[8:], seqNo)
	buf[16] = EventOrderAdd

	// CRC over seqno+type+payload (bytes 8..off)
	checksum := crc32.Checksum(buf[8:off], crcTable)
	binary.LittleEndian.PutUint32(buf[4:], checksum)

	return totalLen
}

// DecodeOrderAdd decodes an order-add event from the payload portion of a record.
// The payload starts after the 17-byte header.
func DecodeOrderAdd(payload []byte) (*models.Order, error) {
	if len(payload) < 8 {
		return nil, fmt.Errorf("payload too short for order add")
	}

	off := 0
	id := binary.LittleEndian.Uint64(payload[off:])
	off += 8

	clientID, n := getString(payload[off:])
	off += n
	instrument, n := getString(payload[off:])
	off += n

	side := models.Side(int8(payload[off]))
	off++

	orderType, n := getString(payload[off:])
	off += n

	price := int64(binary.LittleEndian.Uint64(payload[off:]))
	off += 8
	stopPrice := int64(binary.LittleEndian.Uint64(payload[off:]))
	off += 8
	quantity := binary.LittleEndian.Uint64(payload[off:])
	off += 8

	timeInForce, n := getString(payload[off:])
	off += n

	createdNano := int64(binary.LittleEndian.Uint64(payload[off:]))

	return &models.Order{
		ID:          id,
		ClientID:    clientID,
		Instrument:  instrument,
		Side:        side,
		Type:        models.OrderType(orderType),
		Status:      models.StatusNew,
		Price:       price,
		StopPrice:   stopPrice,
		Quantity:    quantity,
		TimeInForce: timeInForce,
		CreatedAt:   time.Unix(0, createdNano).UTC(),
		UpdatedAt:   time.Unix(0, createdNano).UTC(),
	}, nil
}

// EncodeStopActivation encodes a stop order activation event.
// Uses the same payload format as EncodeOrderAdd but with EventStopActivation type.
func EncodeStopActivation(buf []byte, seqNo uint64, order *models.Order) int {
	n := EncodeOrderAdd(buf, seqNo, order)
	// Override the event type byte
	buf[16] = EventStopActivation
	// Recompute CRC since type byte changed
	checksum := crc32.Checksum(buf[8:n], crcTable)
	binary.LittleEndian.PutUint32(buf[4:], checksum)
	return n
}

// EncodeOrderCancel encodes an order-cancel event into buf.
// Returns the number of bytes written.
func EncodeOrderCancel(buf []byte, seqNo uint64, orderID uint64, instrument string) int {
	off := headerSize

	binary.LittleEndian.PutUint64(buf[off:], orderID)
	off += 8
	off += putString(buf[off:], instrument)

	totalLen := off
	binary.LittleEndian.PutUint32(buf[0:], uint32(totalLen))
	binary.LittleEndian.PutUint64(buf[8:], seqNo)
	buf[16] = EventOrderCancel

	checksum := crc32.Checksum(buf[8:off], crcTable)
	binary.LittleEndian.PutUint32(buf[4:], checksum)

	return totalLen
}

// DecodeOrderCancel decodes an order-cancel event from the payload.
func DecodeOrderCancel(payload []byte) (orderID uint64, instrument string, err error) {
	if len(payload) < 10 {
		return 0, "", fmt.Errorf("payload too short for order cancel")
	}
	orderID = binary.LittleEndian.Uint64(payload[0:])
	instrument, _ = getString(payload[8:])
	return orderID, instrument, nil
}

// DecodeRecord parses a raw WAL record and returns its seqNo, event type, and payload.
// Returns an error if the CRC check fails or the record is malformed.
func DecodeRecord(buf []byte) (seqNo uint64, eventType uint8, payload []byte, err error) {
	if len(buf) < headerSize {
		return 0, 0, nil, fmt.Errorf("record too short: %d bytes", len(buf))
	}

	totalLen := binary.LittleEndian.Uint32(buf[0:])
	if int(totalLen) > len(buf) {
		return 0, 0, nil, fmt.Errorf("record length %d exceeds buffer %d", totalLen, len(buf))
	}

	storedCRC := binary.LittleEndian.Uint32(buf[4:])
	seqNo = binary.LittleEndian.Uint64(buf[8:])
	eventType = buf[16]
	payload = buf[headerSize:totalLen]

	// Verify CRC over seqno+type+payload (bytes 8..totalLen)
	computed := crc32.Checksum(buf[8:totalLen], crcTable)
	if computed != storedCRC {
		return 0, 0, nil, fmt.Errorf("CRC mismatch: stored=%08x computed=%08x", storedCRC, computed)
	}

	return seqNo, eventType, payload, nil
}

// putString writes a length-prefixed string. Returns bytes written.
func putString(buf []byte, s string) int {
	binary.LittleEndian.PutUint16(buf, uint16(len(s)))
	copy(buf[2:], s)
	return 2 + len(s)
}

// getString reads a length-prefixed string. Returns the string and bytes consumed.
func getString(buf []byte) (string, int) {
	l := int(binary.LittleEndian.Uint16(buf))
	return string(buf[2 : 2+l]), 2 + l
}
