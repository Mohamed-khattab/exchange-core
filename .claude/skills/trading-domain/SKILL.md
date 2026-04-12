---
name: trading-domain
description: >
  TRIGGER when: discussing order types, matching algorithms, market microstructure,
  exchange design, trading terminology, price-time priority, order book behavior,
  or financial exchange concepts. Activates for domain questions and feature design.
---

You are operating as a Principal Exchange Systems Architect with 15+ years of experience building electronic trading venues, matching engines, and market data systems for equities, crypto, and derivatives markets.

## Order Types — Behavior & Semantics

### Currently Implemented

| Type | Behavior | Rests in Book? | Partial Fill? |
|------|----------|---------------|---------------|
| **LIMIT** | Match at specified price or better, rest remainder | Yes | Yes |
| **MARKET** | Match at best available prices, reject if no liquidity | No | Yes (remainder rejected) |
| **IOC** | Match what's available, cancel remainder immediately | No | Yes (remainder cancelled) |
| **FOK** | All-or-nothing — check liquidity first, match or cancel entirely | No | No |

### Planned / Extension Candidates

| Type | Behavior | Implementation Notes |
|------|----------|---------------------|
| **STOP-LIMIT** | Becomes LIMIT when trigger price is hit | Needs triggered-order queue, price monitoring per tick |
| **STOP-MARKET** | Becomes MARKET when trigger price is hit | Same trigger mechanism, simpler execution |
| **ICEBERG** | Shows only a visible portion, replenishes from hidden qty | Add `VisibleQty` + `HiddenQty` fields, replenish on fill |
| **POST-ONLY** | Reject if it would immediately match (maker-only) | Check crossing before insert, reject instead of match |
| **TRAILING-STOP** | Stop price follows market at a fixed offset | Requires continuous price tracking per order |
| **GTD** | Good-Till-Date — auto-cancel at expiry time | Timer or periodic sweep of `ExpireAt` field |

## Price-Time Priority (FIFO)

This engine implements strict **price-time priority**:

1. **Price priority**: Better-priced orders match first
   - Bids: highest price wins
   - Asks: lowest price wins
2. **Time priority**: At the same price level, first-in-first-out (FIFO)
   - Maintained by `*list.List` (doubly-linked list) per `PriceLevel`
   - `PushBack()` for new orders, `Front()` for next fill

**Maker price wins**: When an aggressor crosses the book, the fill executes at the **resting order's price** (maker's price), not the aggressor's limit price. This is standard for continuous limit order books.

## Matching Algorithm

```
matchLimit(order):
  while order has remaining qty:
    contra = best opposite side level
    if no contra OR contra doesn't cross order's price:
      break (no more matching possible)
    fillAgainst(order, contra.Peek())  // fill against time-priority head
  if remaining qty > 0:
    addToBook(order)  // rest at the order's limit price

matchMarket(order):
  while order has remaining qty:
    contra = best opposite side level
    if no contra:
      break (no liquidity)
    fillAgainst(order, contra.Peek())
  if not filled: status = REJECTED (no fill) or PARTIALLY_FILLED

matchFOK(order):
  available = sum contra liquidity at crossing prices
  if available < order.qty:
    status = CANCELLED (insufficient liquidity)
    return
  matchLimit(order)  // guaranteed full fill
```

## Key Trading Concepts

### Spread
The difference between the best ask (lowest sell) and best bid (highest buy). A tighter spread indicates higher liquidity.

### VWAP (Volume-Weighted Average Price)
Tracked per order via `AvgFillPrice`. For an order filled across multiple price levels:
```
AvgFillPrice = sum(fill_price_i * fill_qty_i) / total_filled_qty
```

### Aggressor
The party that initiated the match — the incoming order that crosses the book. The resting order is the **maker**. This distinction matters for:
- Fee schedules (maker/taker fees)
- Trade reporting
- Market impact analysis

### Self-Trade Prevention (STP)
Not yet implemented. When the same `ClientID` appears on both sides of a potential fill, the engine should either cancel the incoming order, cancel the resting order, or cancel both. Common in production exchanges.

### Circuit Breakers
Not yet implemented. Production exchanges halt trading when:
- Price moves more than X% in a rolling window
- Order rate exceeds threshold (denial-of-service protection)
- Fat-finger detection (order size >> normal)

## Book Depth & Liquidity

```
Ask side (ascending price):
  $100.05  |  50 qty  |  3 orders    ← best ask (top of book)
  $100.10  |  120 qty |  7 orders
  $100.15  |  30 qty  |  1 order

Bid side (descending price):
  $100.00  |  80 qty  |  5 orders    ← best bid (top of book)
  $99.95   |  200 qty |  12 orders
  $99.90   |  45 qty  |  2 orders

Spread = $100.05 - $100.00 = $0.05
```

## Fixed-Point Price Representation

This engine uses `int64` scaled by `1e8` to avoid floating-point precision errors:

```
$100.05 → 10_005_000_000 (int64)
0.00000001 BTC → 1 (minimum representable unit = 1 satoshi)
```

**Why not float64?**
- `0.1 + 0.2 != 0.3` in IEEE 754
- Price comparisons must be exact for matching correctness
- Accumulated rounding across thousands of fills creates real P&L discrepancies

## Design Decisions for This Engine

| Decision | Rationale |
|----------|-----------|
| Sorted slice for price tree | Cache-friendly, simple, sufficient for <5k levels |
| One goroutine per instrument | Eliminates all lock contention on the hot path |
| Channel-based command routing | Clean separation, built-in back-pressure (10k buffer) |
| WAL-before-mutation | Crash recovery without data loss |
| Atomic ID generation | Lock-free, monotonic, no coordination needed |
| No external dependencies | Deployment simplicity, no version conflicts, smaller attack surface |

## Regulatory Considerations

Production exchanges must typically implement:
- **Audit trail**: Every order event (new, modify, cancel, fill) with nanosecond timestamps
- **Best execution**: Demonstrable price-time priority enforcement
- **Market data fairness**: All participants receive data simultaneously
- **Order-to-trade ratio limits**: Prevent excessive quoting/cancellation
- **Kill switches**: Ability to halt a participant or entire market instantly
