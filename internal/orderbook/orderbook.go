// Package orderbook implements a high-performance limit order book using
// sorted price levels backed by a skip-list style doubly-linked list.
// Each price level maintains a FIFO queue of orders for strict price-time priority.
package orderbook

import (
	"container/list"
	"fmt"
	"sync"
	"sync/atomic"

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
// A sorted set of price levels stored in a sorted slice. For production use,
// replace with a balanced BST (e.g., a red-black tree from a vendored library).
// The slice approach is simple and performs well up to ~5000 distinct price levels,
// which covers the vast majority of real-world order books.

type priceTree struct {
	levels []*PriceLevel // sorted ascending
	index  map[int64]int // price -> position in slice
	isBuy  bool          // buy side iterates in descending order
}

func newPriceTree(isBuy bool) *priceTree {
	return &priceTree{
		levels: make([]*PriceLevel, 0, 64),
		index:  make(map[int64]int),
		isBuy:  isBuy,
	}
}

func (t *priceTree) findInsertPos(price int64) int {
	lo, hi := 0, len(t.levels)
	for lo < hi {
		mid := (lo + hi) / 2
		if t.levels[mid].Price < price {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (t *priceTree) GetOrCreate(price int64) *PriceLevel {
	if pos, ok := t.index[price]; ok {
		return t.levels[pos]
	}
	pl := newPriceLevel(price)
	pos := t.findInsertPos(price)
	// Insert at pos
	t.levels = append(t.levels, nil)
	copy(t.levels[pos+1:], t.levels[pos:])
	t.levels[pos] = pl
	// Rebuild index for shifted entries
	for i := pos; i < len(t.levels); i++ {
		t.index[t.levels[i].Price] = i
	}
	return pl
}

func (t *priceTree) Get(price int64) *PriceLevel {
	if pos, ok := t.index[price]; ok {
		return t.levels[pos]
	}
	return nil
}

func (t *priceTree) Remove(price int64) {
	pos, ok := t.index[price]
	if !ok {
		return
	}
	t.levels = append(t.levels[:pos], t.levels[pos+1:]...)
	delete(t.index, price)
	// Rebuild index for shifted entries
	for i := pos; i < len(t.levels); i++ {
		t.index[t.levels[i].Price] = i
	}
}

// Best returns the best price level (highest bid / lowest ask)
func (t *priceTree) Best() *PriceLevel {
	if len(t.levels) == 0 {
		return nil
	}
	if t.isBuy {
		return t.levels[len(t.levels)-1] // highest bid
	}
	return t.levels[0] // lowest ask
}

func (t *priceTree) Len() int { return len(t.levels) }

// TopN returns top N levels from best to worst price
func (t *priceTree) TopN(n int) []*PriceLevel {
	if n > len(t.levels) {
		n = len(t.levels)
	}
	result := make([]*PriceLevel, n)
	if t.isBuy {
		for i := 0; i < n; i++ {
			result[i] = t.levels[len(t.levels)-1-i]
		}
	} else {
		copy(result, t.levels[:n])
	}
	return result
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
}

func NewOrderBook(instrument string) *OrderBook {
	return &OrderBook{
		instrument: instrument,
		bids:       newPriceTree(true),
		asks:       newPriceTree(false),
		orders:     make(map[uint64]*models.Order, 1024),
	}
}

func (ob *OrderBook) nextSeq() uint64 {
	return atomic.AddUint64(&ob.sequence, 1)
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
		// Cancel any remaining quantity
		if !order.IsFilled() {
			order.Status = models.StatusCancelled
		}
		return results, nil
	case models.OrderTypeFOK:
		return ob.matchFOK(order)
	default:
		return nil, fmt.Errorf("unsupported order type: %s", order.Type)
	}
}

// CancelOrder removes an order from the book. Returns false if order not found.
func (ob *OrderBook) CancelOrder(orderID uint64) (*models.Order, bool) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	order, ok := ob.orders[orderID]
	if !ok {
		return nil, false
	}
	ob.removeFromBook(order)
	order.Status = models.StatusCancelled
	delete(ob.orders, orderID)
	ob.nextSeq()
	return order, true
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

// AllOrders returns a copy of all resting orders in the book.
// Used for snapshot creation.
func (ob *OrderBook) AllOrders() []*models.Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	out := make([]*models.Order, 0, len(ob.orders))
	for _, o := range ob.orders {
		out = append(out, o)
	}
	return out
}

// RestoreOrder inserts an order directly into the book without matching.
// Used during WAL replay/snapshot restore to reconstruct the book state.
func (ob *OrderBook) RestoreOrder(order *models.Order) {
	ob.mu.Lock()
	defer ob.mu.Unlock()
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

		result := ob.fillAgainst(order, contra)
		if result != nil {
			results = append(results, result)
		}
	}

	// Rest the remaining quantity in the book
	if order.RemainingQty() > 0 {
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
		result := ob.fillAgainst(order, contra)
		if result != nil {
			results = append(results, result)
		}
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
		for _, level := range ob.asks.levels {
			if level.Price > order.Price {
				break
			}
			total += level.TotalQty
		}
	} else {
		n := len(ob.bids.levels)
		for i := n - 1; i >= 0; i-- {
			level := ob.bids.levels[i]
			if level.Price < order.Price {
				break
			}
			total += level.TotalQty
		}
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
