package fix

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/trading/matching-engine/internal/engine"
	"github.com/trading/matching-engine/internal/models"
)

// OrderHandler processes FIX application messages and translates them
// to matching engine API calls.
type OrderHandler struct {
	engine *engine.MatchingEngine

	// ClOrdID <-> OrderID mapping per session
	mu          sync.RWMutex
	byClOrdID   map[string]uint64
	byOrderID   map[uint64]string
}

// NewOrderHandler creates a handler backed by the given engine.
func NewOrderHandler(eng *engine.MatchingEngine) *OrderHandler {
	return &OrderHandler{
		engine:    eng,
		byClOrdID: make(map[string]uint64),
		byOrderID: make(map[uint64]string),
	}
}

// HandleMessage dispatches a FIX application message.
func (h *OrderHandler) HandleMessage(s *Session, msg *Message) {
	switch msg.MsgType {
	case MsgTypeNewOrderSingle:
		h.handleNewOrder(s, msg)
	case MsgTypeOrderCancelRequest:
		h.handleCancelRequest(s, msg)
	default:
		log.Printf("[fix] unsupported message type: %s", msg.MsgType)
	}
}

func (h *OrderHandler) handleNewOrder(s *Session, msg *Message) {
	clOrdID := msg.GetField(TagClOrdID)
	symbol := msg.GetField(TagSymbol)
	side := msg.GetField(TagSide)
	ordType := msg.GetField(TagOrdType)

	qty, err := msg.GetFloat(TagOrderQty)
	if err != nil {
		h.sendReject(s, clOrdID, symbol, "invalid OrderQty")
		return
	}

	var price float64
	if ordType == OrdTypeLimit {
		price, err = msg.GetFloat(TagPrice)
		if err != nil {
			h.sendReject(s, clOrdID, symbol, "invalid Price")
			return
		}
	}

	req := &models.OrderRequest{
		ClientID:   clOrdID,
		Instrument: symbol,
		Side:       fixSideToEngine(side),
		Type:       fixOrdTypeToEngine(ordType),
		Price:      price,
		Quantity:   qty,
		ReceivedAt: time.Now(),
	}

	order, trades, err := h.engine.SubmitOrder(req)
	if err != nil {
		h.sendReject(s, clOrdID, symbol, err.Error())
		return
	}

	// Register mapping
	h.mu.Lock()
	h.byClOrdID[clOrdID] = order.ID
	h.byOrderID[order.ID] = clOrdID
	h.mu.Unlock()

	// Send execution report for the new order
	h.sendExecutionReport(s, order, ExecTypeNew, clOrdID)

	// Send execution reports for any immediate fills
	for _, r := range trades {
		h.sendTradeReport(s, order, r.Trade, clOrdID)
	}
}

func (h *OrderHandler) handleCancelRequest(s *Session, msg *Message) {
	origClOrdID := msg.GetField(TagOrigClOrdID)
	symbol := msg.GetField(TagSymbol)

	h.mu.RLock()
	orderID, ok := h.byClOrdID[origClOrdID]
	h.mu.RUnlock()
	if !ok {
		h.sendReject(s, origClOrdID, symbol, "unknown ClOrdID")
		return
	}

	order, err := h.engine.CancelOrder(symbol, orderID)
	if err != nil {
		h.sendReject(s, origClOrdID, symbol, err.Error())
		return
	}

	h.sendExecutionReport(s, order, ExecTypeCancelled, origClOrdID)
}

func (h *OrderHandler) sendExecutionReport(s *Session, order *models.Order, execType string, clOrdID string) {
	msg := NewMessage(MsgTypeExecutionReport)
	msg.SetField(TagOrderID, fmt.Sprintf("%d", order.ID))
	msg.SetField(TagClOrdID, clOrdID)
	msg.SetField(TagExecID, fmt.Sprintf("E%d", order.ID))
	msg.SetField(TagExecType, execType)
	msg.SetField(TagOrdStatus, engineStatusToFix(order.Status))
	msg.SetField(TagSymbol, order.Instrument)
	msg.SetField(TagSide, engineSideToFix(order.Side))
	msg.SetField(TagOrderQty, fmt.Sprintf("%.8f", models.QtyToFloat(order.Quantity)))
	msg.SetField(TagLeavesQty, fmt.Sprintf("%.8f", models.QtyToFloat(order.RemainingQty())))
	msg.SetField(TagCumQty, fmt.Sprintf("%.8f", models.QtyToFloat(order.FilledQty)))
	s.Send(msg)
}

func (h *OrderHandler) sendTradeReport(s *Session, order *models.Order, trade *models.Trade, clOrdID string) {
	msg := NewMessage(MsgTypeExecutionReport)
	msg.SetField(TagOrderID, fmt.Sprintf("%d", order.ID))
	msg.SetField(TagClOrdID, clOrdID)
	msg.SetField(TagExecID, fmt.Sprintf("T%d", trade.ID))
	msg.SetField(TagExecType, ExecTypeTrade)
	msg.SetField(TagOrdStatus, engineStatusToFix(order.Status))
	msg.SetField(TagSymbol, order.Instrument)
	msg.SetField(TagSide, engineSideToFix(order.Side))
	msg.SetField(TagOrderQty, fmt.Sprintf("%.8f", models.QtyToFloat(order.Quantity)))
	msg.SetField(TagLastPx, fmt.Sprintf("%.8f", models.PriceToFloat(trade.Price)))
	msg.SetField(TagLastQty, fmt.Sprintf("%.8f", models.QtyToFloat(trade.Quantity)))
	msg.SetField(TagLeavesQty, fmt.Sprintf("%.8f", models.QtyToFloat(order.RemainingQty())))
	msg.SetField(TagCumQty, fmt.Sprintf("%.8f", models.QtyToFloat(order.FilledQty)))
	s.Send(msg)
}

func (h *OrderHandler) sendReject(s *Session, clOrdID, symbol, reason string) {
	msg := NewMessage(MsgTypeExecutionReport)
	msg.SetField(TagClOrdID, clOrdID)
	msg.SetField(TagExecID, "REJ")
	msg.SetField(TagExecType, ExecTypeRejected)
	msg.SetField(TagOrdStatus, OrdStatusRejected)
	msg.SetField(TagSymbol, symbol)
	msg.SetField(58, reason) // tag 58 = Text
	s.Send(msg)
}

func fixSideToEngine(fixSide string) string {
	switch fixSide {
	case SideBuy:
		return "BUY"
	case SideSell:
		return "SELL"
	default:
		return fixSide
	}
}

func fixOrdTypeToEngine(fixType string) string {
	switch fixType {
	case OrdTypeMarket:
		return "MARKET"
	case OrdTypeLimit:
		return "LIMIT"
	default:
		return fixType
	}
}

func engineSideToFix(side models.Side) string {
	if side == models.SideBuy {
		return SideBuy
	}
	return SideSell
}

func engineStatusToFix(status models.OrderStatus) string {
	switch status {
	case models.StatusNew:
		return OrdStatusNew
	case models.StatusPartiallyFilled:
		return OrdStatusPartiallyFilled
	case models.StatusFilled:
		return OrdStatusFilled
	case models.StatusCancelled:
		return OrdStatusCancelled
	default:
		return OrdStatusRejected
	}
}
