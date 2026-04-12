package models

import (
	"fmt"
	"sync/atomic"
	"time"
)

// ── Side ──────────────────────────────────────────────────────────────────────

type Side int8

const (
	SideBuy  Side = 1
	SideSell Side = -1
)

func (s Side) String() string {
	if s == SideBuy {
		return "BUY"
	}
	return "SELL"
}

func (s Side) Opposite() Side {
	return Side(-int8(s))
}

// ── OrderType ─────────────────────────────────────────────────────────────────

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
	OrderTypeIOC    OrderType = "IOC"    // Immediate-or-Cancel
	OrderTypeFOK    OrderType = "FOK"    // Fill-or-Kill
	OrderTypeStop      OrderType = "STOP"       // Stop-Market
	OrderTypeStopLimit OrderType = "STOP_LIMIT" // Stop-Limit
)

// ── STPMode ──────────────────────────────────────────────────────────────────

type STPMode string

const (
	STPNone           STPMode = ""
	STPCancelResting  STPMode = "CANCEL_RESTING"
	STPCancelIncoming STPMode = "CANCEL_INCOMING"
	STPCancelBoth     STPMode = "CANCEL_BOTH"
)

// ── OrderStatus ───────────────────────────────────────────────────────────────

type OrderStatus string

const (
	StatusNew             OrderStatus = "NEW"
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	StatusFilled          OrderStatus = "FILLED"
	StatusCancelled       OrderStatus = "CANCELLED"
	StatusRejected        OrderStatus = "REJECTED"
	StatusExpired         OrderStatus = "EXPIRED"
	StatusSTPCancelled    OrderStatus = "STP_CANCELLED"
	StatusPendingTrigger  OrderStatus = "PENDING_TRIGGER"
)

// ── Order ─────────────────────────────────────────────────────────────────────

var globalOrderID uint64

func NextOrderID() uint64 {
	return atomic.AddUint64(&globalOrderID, 1)
}

// SetMinOrderID sets the global order ID counter to at least the given value.
// Used during WAL replay to prevent ID collisions.
func SetMinOrderID(id uint64) {
	for {
		old := atomic.LoadUint64(&globalOrderID)
		if id <= old {
			return
		}
		if atomic.CompareAndSwapUint64(&globalOrderID, old, id) {
			return
		}
	}
}

type Order struct {
	ID           uint64      `json:"id"`
	ClientID     string      `json:"client_id"`      // client-assigned idempotency key
	Instrument   string      `json:"instrument"`
	Side         Side        `json:"side"`
	Type         OrderType   `json:"type"`
	Status       OrderStatus `json:"status"`
	Price        int64       `json:"price"`           // fixed-point, scaled by 1e8
	StopPrice    int64       `json:"stop_price"`      // for stop orders
	Quantity     uint64      `json:"quantity"`        // in base asset units (e.g., satoshis)
	FilledQty    uint64      `json:"filled_qty"`
	AvgFillPrice int64       `json:"avg_fill_price"`  // volume-weighted avg fill price
	STPMode      STPMode     `json:"stp_mode,omitempty"`
	TimeInForce  string      `json:"time_in_force"`   // GTC, IOC, FOK, GTD
	ExpireAt     time.Time   `json:"expire_at,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

func NewOrder(instrument string, side Side, oType OrderType, price, stopPrice int64, qty uint64, clientID string) *Order {
	now := time.Now().UTC()
	return &Order{
		ID:          NextOrderID(),
		ClientID:    clientID,
		Instrument:  instrument,
		Side:        side,
		Type:        oType,
		Status:      StatusNew,
		Price:       price,
		StopPrice:   stopPrice,
		Quantity:    qty,
		TimeInForce: "GTC",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func (o *Order) RemainingQty() uint64 {
	return o.Quantity - o.FilledQty
}

func (o *Order) IsFilled() bool {
	return o.FilledQty >= o.Quantity
}

func (o *Order) String() string {
	return fmt.Sprintf("Order{id=%d client=%s %s %s qty=%d filled=%d price=%d}",
		o.ID, o.ClientID, o.Side, o.Type, o.Quantity, o.FilledQty, o.Price)
}

// ── Trade ─────────────────────────────────────────────────────────────────────

var globalTradeID uint64

func NextTradeID() uint64 {
	return atomic.AddUint64(&globalTradeID, 1)
}

// SetMinTradeID sets the global trade ID counter to at least the given value.
// Used during WAL replay to prevent ID collisions.
func SetMinTradeID(id uint64) {
	for {
		old := atomic.LoadUint64(&globalTradeID)
		if id <= old {
			return
		}
		if atomic.CompareAndSwapUint64(&globalTradeID, old, id) {
			return
		}
	}
}

type Trade struct {
	ID           uint64    `json:"id"`
	Instrument   string    `json:"instrument"`
	BuyOrderID   uint64    `json:"buy_order_id"`
	SellOrderID  uint64    `json:"sell_order_id"`
	BuyClientID  string    `json:"buy_client_id"`
	SellClientID string    `json:"sell_client_id"`
	Price        int64     `json:"price"`
	Quantity     uint64    `json:"quantity"`
	Timestamp    time.Time `json:"timestamp"`
	Aggressor    Side      `json:"aggressor"` // which side initiated the match
}

func NewTrade(instrument string, buyOrder, sellOrder *Order, price int64, qty uint64, aggressor Side) *Trade {
	return &Trade{
		ID:           NextTradeID(),
		Instrument:   instrument,
		BuyOrderID:   buyOrder.ID,
		SellOrderID:  sellOrder.ID,
		BuyClientID:  buyOrder.ClientID,
		SellClientID: sellOrder.ClientID,
		Price:        price,
		Quantity:     qty,
		Timestamp:    time.Now().UTC(),
		Aggressor:    aggressor,
	}
}

// ── OrderRequest / Response ───────────────────────────────────────────────────

type OrderRequest struct {
	ClientID   string    `json:"client_id"`
	Instrument string    `json:"instrument"`
	Side       string    `json:"side"`       // "BUY" | "SELL"
	Type       string    `json:"type"`       // "LIMIT" | "MARKET" | "IOC" | "FOK" | "STOP" | "STOP_LIMIT"
	Price      float64   `json:"price"`
	StopPrice  float64   `json:"stop_price,omitempty"`
	Quantity   float64   `json:"quantity"`
	STPMode    string    `json:"stp_mode,omitempty"` // "CANCEL_RESTING" | "CANCEL_INCOMING" | "CANCEL_BOTH"
	ExpireAt   time.Time `json:"expire_at,omitempty"`
}

type AmendRequest struct {
	OrderID    uint64  `json:"order_id"`
	Instrument string  `json:"instrument"`
	Price      float64 `json:"price,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
}

// ParseSide converts a string to a Side value.
func ParseSide(s string) (Side, error) {
	switch s {
	case "BUY":
		return SideBuy, nil
	case "SELL":
		return SideSell, nil
	default:
		return 0, fmt.Errorf("invalid side: %s (must be BUY or SELL)", s)
	}
}

// MassCancelFilter specifies criteria for cancelling multiple orders.
type MassCancelFilter struct {
	Instrument string
	ClientID   string
	Side       *Side // nil = any side
}

type CancelRequest struct {
	OrderID    uint64 `json:"order_id"`
	Instrument string `json:"instrument"`
}

type OrderBookLevel struct {
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`
	Orders   int     `json:"orders"`
}

type OrderBookSnapshot struct {
	Instrument string           `json:"instrument"`
	Bids       []OrderBookLevel `json:"bids"` // descending price
	Asks       []OrderBookLevel `json:"asks"` // ascending price
	Timestamp  time.Time        `json:"timestamp"`
	Sequence   uint64           `json:"sequence"`
}

// ── Price scaling helpers ─────────────────────────────────────────────────────

const PriceScale = 1_000_000_00 // 8 decimal places
const QtyScale   = 1_000_000_00 // 8 decimal places (satoshis)

func FloatToPrice(f float64) int64 {
	return int64(f * PriceScale)
}

func PriceToFloat(p int64) float64 {
	return float64(p) / PriceScale
}

func FloatToQty(f float64) uint64 {
	return uint64(f * QtyScale)
}

func QtyToFloat(q uint64) float64 {
	return float64(q) / QtyScale
}
