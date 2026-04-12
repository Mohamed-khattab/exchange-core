// Package orderbook implements a high-performance limit order book using
// sorted price levels backed by a skip-list style doubly-linked list.
// Each price level maintains a FIFO queue of orders for strict price-time priority.
package orderbook

import (
	"container/list"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/trading/matching-engine/internal/models"
)

// ── PriceLevel ────────────────────────────────────────────────────────────────

// PriceLevel holds all orders at a single price point.
// Orders are maintained in FIFO order for time priority within the same price.
type PriceLevel struct {
	Price     int64
	TotalQty  uint64
	Orders    *list.List // *models.Order
	orderMap  map[uint64]*list.Element
}

func newPriceLevel(price int64) *PriceLevel {
	return &PriceLevel{
		Price:    price,
		Orders:   list.New(),
		orderMap: make(map[uint64]*list.Element),
	}
}

func (pl *PriceLevel) Add(o *models.Order) {
	elem := pl.Orders.PushBack(o)
	pl.orderMap[o.ID] = elem
	pl.TotalQty += o.RemainingQty()
}

func (pl *PriceLevel) Remove(o *models.Order) bool {
	elem, ok := pl.orderMap[o.ID]
	if !ok {
		return false
	}
	pl.TotalQty -= o.RemainingQty()
	pl.Orders.Remove(elem)
	delete(pl.orderMap, o.ID)
	return true
}

func (pl *PriceLevel) UpdateQty(orderID uint64, delta int64) {
	if delta < 0 {
		d := uint64(-delta)
		if pl.TotalQty >= d {
			pl.TotalQty -= d
		}
	} else {
		pl.TotalQty += uint64(delta)
	}
}

func (pl *PriceLevel) Peek() *models.Order {
	front := pl.Orders.Front()
	if front == nil {
		return nil
	}
	return front.Value.(*models.Order)
}

func (pl *PriceLevel) IsEmpty() bool {
	return pl.Orders.Len() == 0
}

// ── priceTree ─────────────────────────────────────────────────────────────────
// A balanced red-black tree of price levels providing O(log n) insert/remove.
// Wraps rbTree and provides the same interface as the previous sorted-slice
// implementation.

type priceTree struct {
	tree  *rbTree
	isBuy bool // buy side: best = max; sell side: best = min
}

func newPriceTree(isBuy bool) *priceTree {
	return &priceTree{
		tree:  newRBTree(),
		isBuy: isBuy,
	}
}

func (t *priceTree) GetOrCreate(price int64) *PriceLevel {
	if n := t.tree.Find(price); n != nil {
		return n.level
	}
	pl := newPriceLevel(price)
	t.tree.Insert(price, pl)
	return pl
}

func (t *priceTree) Get(price int64) *PriceLevel {
	if n := t.tree.Find(price); n != nil {
		return n.level
	}
	return nil
}

func (t *priceTree) Remove(price int64) {
	t.tree.Delete(price)
}

// Best returns the best price level (highest bid / lowest ask).
func (t *priceTree) Best() *PriceLevel {
	var n *rbNode
	if t.isBuy {
		n = t.tree.Max()
	} else {
		n = t.tree.Min()
	}
	if n == nil {
		return nil
	}
	return n.level
}

func (t *priceTree) Len() int { return t.tree.Len() }

// TopN returns top N levels from best to worst price.
func (t *priceTree) TopN(n int) []*PriceLevel {
	total := t.tree.Len()
	if n > total {
		n = total
	}
	result := make([]*PriceLevel, 0, n)
	if t.isBuy {
		// Descending from max
		node := t.tree.Max()
		for i := 0; i < n && node != nil; i++ {
			result = append(result, node.level)
			node = t.tree.Predecessor(node)
		}
	} else {
		// Ascending from min
		node := t.tree.Min()
		for i := 0; i < n && node != nil; i++ {
			result = append(result, node.level)
			node = t.tree.Successor(node)
		}
	}
	return result
}

// ForEachAscending iterates levels in ascending price order.
// Stops if fn returns false.
func (t *priceTree) ForEachAscending(fn func(*PriceLevel) bool) {
	t.tree.ForEachAscending(fn)
}

// ForEachDescending iterates levels in descending price order.
// Stops if fn returns false.
func (t *priceTree) ForEachDescending(fn func(*PriceLevel) bool) {
	t.tree.ForEachDescending(fn)
}

// ── OrderBook ─────────────────────────────────────────────────────────────────

// MatchResult carries information about a single fill execution.
type MatchResult struct {
	Trade      *models.Trade
	BuyOrder   *models.Order
	SellOrder  *models.Order
}

// OrderBook is a thread-safe, price-time priority order book for one instrument.
type OrderBook struct {
	mu         sync.RWMutex
	instrument string
	bids       *priceTree // buy orders, best = highest
	asks       *priceTree // sell orders, best = lowest
	orders     map[uint64]*models.Order // fast order lookup by ID
	sequence   uint64                   // monotonic sequence for snapshots
	lastPrice  int64
	lastTrade  *models.Trade

	// Self-trade prevention
	stpEnabled bool
	stpDefault models.STPMode

	// Stop orders
	stopBook *StopBook
}

func NewOrderBook(instrument string) *OrderBook {
	return &OrderBook{
		instrument: instrument,
		bids:       newPriceTree(true),
		asks:       newPriceTree(false),
		orders:     make(map[uint64]*models.Order, 1024),
		stopBook:   newStopBook(),
	}
}

func (ob *OrderBook) nextSeq() uint64 {
	return atomic.AddUint64(&ob.sequence, 1)
}

// SetSTP configures self-trade prevention on the order book.
func (ob *OrderBook) SetSTP(enabled bool, defaultMode models.STPMode) {
	ob.stpEnabled = enabled
	ob.stpDefault = defaultMode
}

// stpAction indicates how to resolve a self-trade conflict.
type stpAction int

const (
	stpNone           stpAction = iota
	stpCancelResting
	stpCancelIncoming
	stpCancelBoth
)

// checkSTP determines if a self-trade conflict exists and what action to take.
func (ob *OrderBook) checkSTP(aggressor, resting *models.Order) stpAction {
	if !ob.stpEnabled {
		return stpNone
	}
	if aggressor.ClientID == "" || aggressor.ClientID != resting.ClientID {
		return stpNone
	}
	mode := aggressor.STPMode
	if mode == models.STPNone {
		mode = ob.stpDefault
	}
	if mode == models.STPNone {
		return stpNone
	}
	switch mode {
	case models.STPCancelResting:
		return stpCancelResting
	case models.STPCancelIncoming:
		return stpCancelIncoming
	case models.STPCancelBoth:
		return stpCancelBoth
	}
	return stpNone
}

// cancelRestingOrder removes a resting order from the book and marks it STP_CANCELLED.
func (ob *OrderBook) cancelRestingOrder(order *models.Order, level *PriceLevel) {
	level.Remove(order)
	delete(ob.orders, order.ID)
	order.Status = models.StatusSTPCancelled
	if level.IsEmpty() {
		if order.Side == models.SideBuy {
			ob.bids.Remove(level.Price)
		} else {
			ob.asks.Remove(level.Price)
		}
	}
	ob.nextSeq()
}

// ── Public methods (all acquire the lock) ────────────────────────────────────

// AddOrder inserts a new limit/stop order into the book.
// Returns the list of matched trades (may be empty if no crossing).
func (ob *OrderBook) AddOrder(order *models.Order) ([]*MatchResult, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	switch order.Type {
	case models.OrderTypeMarket:
		return ob.matchMarket(order)
	case models.OrderTypeLimit:
		return ob.matchLimit(order)
	case models.OrderTypeIOC:
		results, _ := ob.matchLimit(order)
		// Cancel any remaining quantity (but don't overwrite STP status)
		if !order.IsFilled() && order.Status != models.StatusSTPCancelled {
			order.Status = models.StatusCancelled
		}
		return results, nil
	case models.OrderTypeFOK:
		return ob.matchFOK(order)
	case models.OrderTypeStop, models.OrderTypeStopLimit:
		order.Status = models.StatusPendingTrigger
		ob.stopBook.Add(order)
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported order type: %s", order.Type)
	}
}

// CancelOrder removes an order from the book or stop book. Returns false if not found.
func (ob *OrderBook) CancelOrder(orderID uint64) (*models.Order, bool) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	// Check main book first
	order, ok := ob.orders[orderID]
	if ok {
		ob.removeFromBook(order)
		order.Status = models.StatusCancelled
		delete(ob.orders, orderID)
		ob.nextSeq()
		return order, true
	}

	// Check stop book
	order, ok = ob.stopBook.Remove(orderID)
	if ok {
		order.Status = models.StatusCancelled
		ob.nextSeq()
		return order, true
	}

	return nil, false
}

// CheckStops returns stop orders triggered by the current lastPrice.
// Triggered orders are removed from the stop book.
func (ob *OrderBook) CheckStops() []*models.Order {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	return ob.stopBook.TriggeredOrders(ob.lastPrice)
}

// AmendOrder modifies the price and/or quantity of a resting LIMIT order.
// newPrice=0 means keep current price. newQty=0 means keep current quantity.
// Returns the amended order, any trades (if cancel+re-insert caused matching), and an error.
func (ob *OrderBook) AmendOrder(orderID uint64, newPrice int64, newQty uint64) (*models.Order, []*MatchResult, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	order, ok := ob.orders[orderID]
	if !ok {
		return nil, nil, fmt.Errorf("order %d not found", orderID)
	}

	// Only resting LIMIT orders can be amended
	if order.Type != models.OrderTypeLimit {
		return nil, nil, fmt.Errorf("can only amend LIMIT orders (got %s)", order.Type)
	}
	if order.Status != models.StatusNew && order.Status != models.StatusPartiallyFilled {
		return nil, nil, fmt.Errorf("cannot amend order with status %s", order.Status)
	}

	// Resolve defaults
	if newPrice == 0 {
		newPrice = order.Price
	}
	if newQty == 0 {
		newQty = order.Quantity
	}

	// Cannot reduce below filled quantity
	if newQty <= order.FilledQty {
		return nil, nil, fmt.Errorf("new quantity must be greater than filled quantity (%d)",
			order.FilledQty)
	}

	// No-op check
	if newPrice == order.Price && newQty == order.Quantity {
		return order, nil, nil
	}

	priceUnchanged := newPrice == order.Price
	qtyDecreaseOnly := newQty < order.Quantity

	if priceUnchanged && qtyDecreaseOnly {
		// In-place amendment: preserve time priority
		delta := int64(order.Quantity) - int64(newQty)
		order.Quantity = newQty
		// Update price level total
		if order.Side == models.SideBuy {
			if level := ob.bids.Get(order.Price); level != nil {
				level.UpdateQty(orderID, -delta)
			}
		} else {
			if level := ob.asks.Get(order.Price); level != nil {
				level.UpdateQty(orderID, -delta)
			}
		}
		order.UpdatedAt = time.Now().UTC()
		ob.nextSeq()
		return order, nil, nil
	}

	// Cancel + re-insert: lose time priority
	ob.removeFromBook(order)
	delete(ob.orders, orderID)

	order.Price = newPrice
	order.Quantity = newQty
	order.UpdatedAt = time.Now().UTC()
	if order.FilledQty > 0 {
		order.Status = models.StatusPartiallyFilled
	} else {
		order.Status = models.StatusNew
	}

	// Re-enter matching (may produce trades)
	results, _ := ob.matchLimit(order)
	return order, results, nil
}

// MassCancel cancels all orders matching the given filter.
// Returns the list of cancelled orders.
func (ob *OrderBook) MassCancel(filter models.MassCancelFilter) []*models.Order {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	var cancelled []*models.Order

	// Collect matching order IDs from main book (iterate then delete)
	var toCancel []uint64
	for id, order := range ob.orders {
		if !matchesFilter(order, filter) {
			continue
		}
		toCancel = append(toCancel, id)
	}
	for _, id := range toCancel {
		order := ob.orders[id]
		ob.removeFromBook(order)
		order.Status = models.StatusCancelled
		delete(ob.orders, id)
		cancelled = append(cancelled, order)
	}

	// Also cancel matching stop orders
	var stopToCancel []uint64
	for id, order := range ob.stopBook.orders {
		if !matchesFilter(order, filter) {
			continue
		}
		stopToCancel = append(stopToCancel, id)
	}
	for _, id := range stopToCancel {
		order, ok := ob.stopBook.Remove(id)
		if ok {
			order.Status = models.StatusCancelled
			cancelled = append(cancelled, order)
		}
	}

	if len(cancelled) > 0 {
		ob.nextSeq()
	}

	return cancelled
}

func matchesFilter(order *models.Order, filter models.MassCancelFilter) bool {
	if filter.ClientID != "" && order.ClientID != filter.ClientID {
		return false
	}
	if filter.Side != nil && order.Side != *filter.Side {
		return false
	}
	return true
}

// GetOrder retrieves an order by ID.
func (ob *OrderBook) GetOrder(orderID uint64) (*models.Order, bool) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	o, ok := ob.orders[orderID]
	return o, ok
}

// Snapshot returns the current book depth.
func (ob *OrderBook) Snapshot(depth int) *models.OrderBookSnapshot {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	snap := &models.OrderBookSnapshot{
		Instrument: ob.instrument,
		Sequence:   ob.sequence,
		Bids:       ob.levelToSnapshot(ob.bids.TopN(depth)),
		Asks:       ob.levelToSnapshot(ob.asks.TopN(depth)),
	}
	return snap
}

// Stats returns basic book statistics.
func (ob *OrderBook) Stats() (bidLevels, askLevels, openOrders int, bestBid, bestAsk, last int64) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	bidLevels = ob.bids.Len()
	askLevels = ob.asks.Len()
	openOrders = len(ob.orders)
	if b := ob.bids.Best(); b != nil {
		bestBid = b.Price
	}
	if a := ob.asks.Best(); a != nil {
		bestAsk = a.Price
	}
	last = ob.lastPrice
	return
}

// AllOrders returns a copy of all resting orders and pending stop orders.
// Used for snapshot creation.
func (ob *OrderBook) AllOrders() []*models.Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	out := make([]*models.Order, 0, len(ob.orders)+ob.stopBook.Len())
	for _, o := range ob.orders {
		out = append(out, o)
	}
	out = append(out, ob.stopBook.AllOrders()...)
	return out
}

// RestoreOrder inserts an order directly into the book without matching.
// Stop orders with PENDING_TRIGGER status are routed to the stop book.
func (ob *OrderBook) RestoreOrder(order *models.Order) {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	if (order.Type == models.OrderTypeStop || order.Type == models.OrderTypeStopLimit) &&
		order.Status == models.StatusPendingTrigger {
		ob.stopBook.Add(order)
		return
	}
	ob.addToBook(order)
}

// ── Internal matching logic ───────────────────────────────────────────────────

func (ob *OrderBook) matchLimit(order *models.Order) ([]*MatchResult, error) {
	var results []*MatchResult

	for order.RemainingQty() > 0 {
		var contra *PriceLevel
		if order.Side == models.SideBuy {
			contra = ob.asks.Best()
			if contra == nil || contra.Price > order.Price {
				break // no crossing ask
			}
		} else {
			contra = ob.bids.Best()
			if contra == nil || contra.Price < order.Price {
				break // no crossing bid
			}
		}

		// STP check before filling
		resting := contra.Peek()
		if resting == nil {
			break
		}
		action := ob.checkSTP(order, resting)
		switch action {
		case stpCancelResting:
			ob.cancelRestingOrder(resting, contra)
			continue // try next resting order
		case stpCancelIncoming:
			order.Status = models.StatusSTPCancelled
			return results, nil
		case stpCancelBoth:
			ob.cancelRestingOrder(resting, contra)
			order.Status = models.StatusSTPCancelled
			return results, nil
		}

		result := ob.fillAgainst(order, contra)
		if result != nil {
			results = append(results, result)
		}
	}

	// Rest the remaining quantity in the book
	if order.RemainingQty() > 0 && order.Status != models.StatusSTPCancelled {
		ob.addToBook(order)
		order.Status = models.StatusNew
		if order.FilledQty > 0 {
			order.Status = models.StatusPartiallyFilled
		}
	}
	return results, nil
}

func (ob *OrderBook) matchMarket(order *models.Order) ([]*MatchResult, error) {
	var results []*MatchResult

	for order.RemainingQty() > 0 {
		var contra *PriceLevel
		if order.Side == models.SideBuy {
			contra = ob.asks.Best()
		} else {
			contra = ob.bids.Best()
		}
		if contra == nil {
			break // no liquidity
		}

		// STP check before filling
		resting := contra.Peek()
		if resting == nil {
			break
		}
		action := ob.checkSTP(order, resting)
		switch action {
		case stpCancelResting:
			ob.cancelRestingOrder(resting, contra)
			continue
		case stpCancelIncoming:
			order.Status = models.StatusSTPCancelled
			return results, nil
		case stpCancelBoth:
			ob.cancelRestingOrder(resting, contra)
			order.Status = models.StatusSTPCancelled
			return results, nil
		}

		result := ob.fillAgainst(order, contra)
		if result != nil {
			results = append(results, result)
		}
	}

	if order.Status == models.StatusSTPCancelled {
		return results, nil
	}
	if order.IsFilled() {
		order.Status = models.StatusFilled
	} else if order.FilledQty > 0 {
		order.Status = models.StatusPartiallyFilled
	} else {
		order.Status = models.StatusRejected // no fill possible
	}
	return results, nil
}

func (ob *OrderBook) matchFOK(order *models.Order) ([]*MatchResult, error) {
	// First, check if full fill is possible (scan the book, no mutations)
	available := ob.availableLiquidity(order)
	if available < order.Quantity {
		order.Status = models.StatusCancelled
		return nil, nil
	}
	// Full fill confirmed — now match
	results, err := ob.matchLimit(order)
	return results, err
}

// availableLiquidity sums available quantity on the contra side up to the order's price.
func (ob *OrderBook) availableLiquidity(order *models.Order) uint64 {
	var total uint64
	if order.Side == models.SideBuy {
		ob.asks.ForEachAscending(func(level *PriceLevel) bool {
			if level.Price > order.Price {
				return false
			}
			total += level.TotalQty
			return true
		})
	} else {
		ob.bids.ForEachDescending(func(level *PriceLevel) bool {
			if level.Price < order.Price {
				return false
			}
			total += level.TotalQty
			return true
		})
	}
	return total
}

// fillAgainst executes a fill between the incoming order and the best contra level.
func (ob *OrderBook) fillAgainst(aggressor *models.Order, level *PriceLevel) *MatchResult {
	resting := level.Peek()
	if resting == nil {
		return nil
	}

	fillQty := aggressor.RemainingQty()
	if resting.RemainingQty() < fillQty {
		fillQty = resting.RemainingQty()
	}

	fillPrice := resting.Price // maker's price wins (price-time priority)

	// Update quantities
	aggressor.FilledQty += fillQty
	resting.FilledQty += fillQty
	level.TotalQty -= fillQty

	// Update average fill prices (VWAP)
	ob.updateAvgPrice(aggressor, fillPrice, fillQty)
	ob.updateAvgPrice(resting, fillPrice, fillQty)

	// Determine trade sides
	var buyOrder, sellOrder *models.Order
	if aggressor.Side == models.SideBuy {
		buyOrder, sellOrder = aggressor, resting
	} else {
		buyOrder, sellOrder = resting, aggressor
	}

	// Build trade
	trade := models.NewTrade(ob.instrument, buyOrder, sellOrder, fillPrice, fillQty, aggressor.Side)
	ob.lastPrice = fillPrice
	ob.lastTrade = trade
	ob.nextSeq()

	// Update statuses
	if resting.IsFilled() {
		level.Remove(resting)
		delete(ob.orders, resting.ID)
		resting.Status = models.StatusFilled
		if level.IsEmpty() {
			if resting.Side == models.SideBuy {
				ob.bids.Remove(level.Price)
			} else {
				ob.asks.Remove(level.Price)
			}
		}
	} else {
		resting.Status = models.StatusPartiallyFilled
	}

	if aggressor.IsFilled() {
		aggressor.Status = models.StatusFilled
	} else {
		aggressor.Status = models.StatusPartiallyFilled
	}

	return &MatchResult{Trade: trade, BuyOrder: buyOrder, SellOrder: sellOrder}
}

func (ob *OrderBook) updateAvgPrice(o *models.Order, fillPrice int64, fillQty uint64) {
	prevValue := o.AvgFillPrice * int64(o.FilledQty-fillQty)
	thisValue := fillPrice * int64(fillQty)
	if o.FilledQty > 0 {
		o.AvgFillPrice = (prevValue + thisValue) / int64(o.FilledQty)
	}
}

func (ob *OrderBook) addToBook(order *models.Order) {
	var level *PriceLevel
	if order.Side == models.SideBuy {
		level = ob.bids.GetOrCreate(order.Price)
	} else {
		level = ob.asks.GetOrCreate(order.Price)
	}
	level.Add(order)
	ob.orders[order.ID] = order
}

func (ob *OrderBook) removeFromBook(order *models.Order) {
	if order.Side == models.SideBuy {
		if level := ob.bids.Get(order.Price); level != nil {
			level.Remove(order)
			if level.IsEmpty() {
				ob.bids.Remove(order.Price)
			}
		}
	} else {
		if level := ob.asks.Get(order.Price); level != nil {
			level.Remove(order)
			if level.IsEmpty() {
				ob.asks.Remove(order.Price)
			}
		}
	}
}

func (ob *OrderBook) levelToSnapshot(levels []*PriceLevel) []models.OrderBookLevel {
	out := make([]models.OrderBookLevel, 0, len(levels))
	for _, l := range levels {
		out = append(out, models.OrderBookLevel{
			Price:    models.PriceToFloat(l.Price),
			Quantity: models.QtyToFloat(l.TotalQty),
			Orders:   l.Orders.Len(),
		})
	}
	return out
}
