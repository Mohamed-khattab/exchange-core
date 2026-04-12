# Order Types — Implementation Reference

## Stop-Limit Order Implementation Guide

A stop-limit order has two prices: a **stop price** (trigger) and a **limit price** (execution).

### Data Model Changes
```go
// Already exists in models.Order:
// StopPrice int64 `json:"stop_price"` — trigger price
// Price     int64 `json:"price"`      — limit price once activated
```

### Triggered Order Queue
```go
type instrumentWorker struct {
    // ... existing fields
    stopOrders  map[uint64]*models.Order // orderID → stop order waiting for trigger
}
```

### Trigger Logic (in handleCommand, after each trade)
```go
func (w *instrumentWorker) checkStopTriggers(lastPrice int64) {
    for id, order := range w.stopOrders {
        triggered := false
        if order.Side == models.SideBuy && lastPrice >= order.StopPrice {
            triggered = true  // price rose to stop level
        } else if order.Side == models.SideSell && lastPrice <= order.StopPrice {
            triggered = true  // price fell to stop level
        }
        if triggered {
            delete(w.stopOrders, id)
            order.Type = models.OrderTypeLimit  // convert to limit
            results, _ := w.book.AddOrder(order)
            // record trades, WAL events...
        }
    }
}
```

### WAL Events
- `EventStopOrderAdd` (new event type) — records the stop order placement
- On trigger, write a normal `EventOrderAdd` for the activated limit order

---

## Iceberg Order Implementation Guide

Shows only a portion of the total quantity. When the visible portion fills, it replenishes from the hidden reserve.

### Data Model Changes
```go
type Order struct {
    // ... existing fields
    DisplayQty uint64 `json:"display_qty"` // visible portion (0 = fully visible)
    TotalQty   uint64 `json:"total_qty"`   // full intended quantity
}
```

### Book Behavior
- Insert with `Quantity = DisplayQty` (visible portion only)
- On complete fill of visible portion:
  1. Replenish: `Quantity = min(DisplayQty, TotalQty - FilledQty)`
  2. **Lose time priority** — move to back of queue at that price level
  3. If `TotalQty` fully filled, mark as FILLED

### Key Detail
Iceberg replenishment creates a **new time priority position**. The order moves to the back of the FIFO queue at its price level. This is standard exchange behavior — hiding quantity should not grant perpetual time priority.

---

## Post-Only Order Implementation Guide

A maker-only order that is rejected if it would immediately match.

### Matching Logic
```go
case models.OrderTypePostOnly:
    // Check if the order would cross the book
    if order.Side == models.SideBuy {
        bestAsk := ob.asks.Best()
        if bestAsk != nil && order.Price >= bestAsk.Price {
            order.Status = models.StatusRejected
            return nil, nil  // would have matched — reject
        }
    } else {
        bestBid := ob.bids.Best()
        if bestBid != nil && order.Price <= bestBid.Price {
            order.Status = models.StatusRejected
            return nil, nil
        }
    }
    // No crossing — safe to rest
    ob.addToBook(order)
    order.Status = models.StatusNew
    return nil, nil
```

---

## GTD (Good-Till-Date) Implementation Guide

Orders that automatically expire at a specified time.

### Expiry Sweep
Run a periodic check (e.g., every second) in each instrument worker:

```go
func (w *instrumentWorker) sweepExpired() {
    now := time.Now().UTC()
    for id, order := range w.book.orders {
        if !order.ExpireAt.IsZero() && now.After(order.ExpireAt) {
            w.book.CancelOrder(id)
            // WAL write: EventOrderCancel
            // Metrics: record expiration
        }
    }
}
```

### Timer Integration
Add a `time.Ticker` to the worker's select loop:
```go
ticker := time.NewTicker(1 * time.Second)
select {
case cmd := <-w.cmdCh:
    w.handleCommand(cmd)
case <-ticker.C:
    w.sweepExpired()
case <-w.stopCh:
    return
}
```

---

## Self-Trade Prevention Modes

| Mode | Behavior |
|------|----------|
| `CANCEL_NEWEST` | Cancel the incoming (aggressor) order |
| `CANCEL_OLDEST` | Cancel the resting (maker) order |
| `CANCEL_BOTH` | Cancel both orders |
| `DECREMENT_AND_CANCEL` | Reduce both orders by the overlap, cancel the remainder of the smaller |

### Check Point
In `fillAgainst()`, before executing the fill:
```go
if aggressor.ClientID == resting.ClientID {
    // Apply STP policy
}
```
