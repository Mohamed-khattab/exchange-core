package orderbook

import (
	"sort"

	"github.com/trading/matching-engine/internal/models"
)

// StopBook holds pending stop orders indexed by trigger price.
// Buy stops trigger when lastPrice >= stopPrice (sorted ascending).
// Sell stops trigger when lastPrice <= stopPrice (sorted descending for efficient scan).
type StopBook struct {
	buyStops  []*stopLevel          // sorted by StopPrice ascending
	sellStops []*stopLevel          // sorted by StopPrice ascending (scan descending)
	orders    map[uint64]*models.Order
}

type stopLevel struct {
	StopPrice int64
	Orders    []*models.Order // FIFO within same stop price
}

func newStopBook() *StopBook {
	return &StopBook{
		orders: make(map[uint64]*models.Order),
	}
}

// Add inserts a stop order into the appropriate tree.
func (sb *StopBook) Add(order *models.Order) {
	sb.orders[order.ID] = order
	if order.Side == models.SideBuy {
		sb.buyStops = sb.addToLevels(sb.buyStops, order)
	} else {
		sb.sellStops = sb.addToLevels(sb.sellStops, order)
	}
}

// Remove cancels a pending stop order. Returns the order and true if found.
func (sb *StopBook) Remove(orderID uint64) (*models.Order, bool) {
	order, ok := sb.orders[orderID]
	if !ok {
		return nil, false
	}
	delete(sb.orders, orderID)
	if order.Side == models.SideBuy {
		sb.buyStops = sb.removeFromLevels(sb.buyStops, order)
	} else {
		sb.sellStops = sb.removeFromLevels(sb.sellStops, order)
	}
	return order, true
}

// TriggeredOrders returns all stop orders that should be activated at the given price.
// Triggered orders are removed from the stop book.
func (sb *StopBook) TriggeredOrders(lastPrice int64) []*models.Order {
	var triggered []*models.Order

	// Buy stops: trigger when lastPrice >= stopPrice
	// Scan ascending until stopPrice > lastPrice
	var remainBuy []*stopLevel
	for _, level := range sb.buyStops {
		if level.StopPrice <= lastPrice {
			for _, o := range level.Orders {
				triggered = append(triggered, o)
				delete(sb.orders, o.ID)
			}
		} else {
			remainBuy = append(remainBuy, level)
		}
	}
	sb.buyStops = remainBuy

	// Sell stops: trigger when lastPrice <= stopPrice
	// Scan descending until stopPrice < lastPrice
	var remainSell []*stopLevel
	for i := len(sb.sellStops) - 1; i >= 0; i-- {
		level := sb.sellStops[i]
		if level.StopPrice >= lastPrice {
			for _, o := range level.Orders {
				triggered = append(triggered, o)
				delete(sb.orders, o.ID)
			}
		} else {
			remainSell = append(remainSell, level)
		}
	}
	// remainSell was built in reverse order, reverse it back
	for i, j := 0, len(remainSell)-1; i < j; i, j = i+1, j-1 {
		remainSell[i], remainSell[j] = remainSell[j], remainSell[i]
	}
	sb.sellStops = remainSell

	return triggered
}

// AllOrders returns all pending stop orders.
func (sb *StopBook) AllOrders() []*models.Order {
	out := make([]*models.Order, 0, len(sb.orders))
	for _, o := range sb.orders {
		out = append(out, o)
	}
	return out
}

// Len returns the number of pending stop orders.
func (sb *StopBook) Len() int {
	return len(sb.orders)
}

func (sb *StopBook) addToLevels(levels []*stopLevel, order *models.Order) []*stopLevel {
	// Find or create the level
	idx := sort.Search(len(levels), func(i int) bool {
		return levels[i].StopPrice >= order.StopPrice
	})
	if idx < len(levels) && levels[idx].StopPrice == order.StopPrice {
		levels[idx].Orders = append(levels[idx].Orders, order)
		return levels
	}
	// Insert new level
	newLevel := &stopLevel{StopPrice: order.StopPrice, Orders: []*models.Order{order}}
	levels = append(levels, nil)
	copy(levels[idx+1:], levels[idx:])
	levels[idx] = newLevel
	return levels
}

func (sb *StopBook) removeFromLevels(levels []*stopLevel, order *models.Order) []*stopLevel {
	idx := sort.Search(len(levels), func(i int) bool {
		return levels[i].StopPrice >= order.StopPrice
	})
	if idx >= len(levels) || levels[idx].StopPrice != order.StopPrice {
		return levels
	}
	level := levels[idx]
	for i, o := range level.Orders {
		if o.ID == order.ID {
			level.Orders = append(level.Orders[:i], level.Orders[i+1:]...)
			break
		}
	}
	if len(level.Orders) == 0 {
		levels = append(levels[:idx], levels[idx+1:]...)
	}
	return levels
}
