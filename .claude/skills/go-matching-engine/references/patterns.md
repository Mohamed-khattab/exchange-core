# Patterns Reference — Matching Engine

## Worker Lifecycle

```
NewMatchingEngine()
  └─ newInstrumentWorker() per instrument
      ├─ creates OrderBook
      ├─ creates cmdCh (buffered 10k)
      └─ creates WAL writer (if enabled)

engine.Start()
  ├─ worker.recover() — replay WAL + snapshot
  └─ go worker.run() — blocking select loop

engine.Stop()
  └─ worker.stop()
      ├─ close(stopCh)
      ├─ <-done  (wait for goroutine exit)
      └─ walWriter.Close()
```

## Order Flow Through the System

```
HTTP POST /v1/orders
  → api.ordersHandler()
    → validateOrderRequest()
    → engine.SubmitOrder(req)
      → models.NewOrder() — atomic ID assignment
      → worker.submit(command)
        → cmdCh <- cmd (non-blocking, back-pressure on full)
      → <-respCh (blocking wait for result)
        → worker.handleCommand()
          → WAL write (if enabled)
          → orderbook.AddOrder()
            → matchLimit() / matchMarket() / matchFOK() / matchIOC()
              → fillAgainst() — per-fill execution
                → models.NewTrade() — atomic trade ID
            → addToBook() if resting quantity remains
          → metrics recording
          → respCh <- result
    → createdResp(w, {order, trades})
```

## Price Level Data Structure

```
priceTree (sorted slice of price levels)
├── levels: []*PriceLevel  — sorted ascending by price
├── index: map[int64]int   — price → position (O(1) lookup)
└── isBuy: bool            — determines iteration direction

PriceLevel
├── Price: int64
├── TotalQty: uint64
├── Orders: *list.List     — doubly-linked, FIFO queue
└── orderMap: map[uint64]*list.Element  — O(1) cancel by ID
```

**Buy side**: best = highest price = `levels[len-1]`
**Sell side**: best = lowest price = `levels[0]`

## Snapshot & Recovery Flow

```
WAL Write Path (per command):
  1. walWriter.NextSeqNo()
  2. EncodeOrderAdd/Cancel(walBuf, seqNo, order)
  3. walWriter.Append(walBuf[:n])
  4. eventCount++
  5. maybeSnapshot() — if eventCount >= snapshotEvery
     a. book.AllOrders()
     b. wal.WriteSnapshot(dir, seqNo, orders)
     c. wal.CleanOldFiles(dir, seqNo)

Recovery Path (on Start):
  1. wal.LoadSnapshot(dir) → (snapSeq, orders)
  2. book.RestoreOrder() for each — no matching
  3. reader.Replay(afterSeqNo, callback)
     a. DecodeOrderAdd → book.AddOrder() — with matching
     b. DecodeOrderCancel → book.CancelOrder()
  4. SetMinOrderID/SetMinTradeID — prevent ID collisions
  5. walWriter.SetSeqNo(maxSeq) — continue sequence
```

## Adding a New Engine Command

When adding a new command type (e.g., `cmdModifyOrder`):

1. Add to `cmdType` iota in `engine.go`
2. Add fields to `command` struct if needed
3. Add case in `instrumentWorker.handleCommand()`
4. Add WAL event type + encoding if durable
5. Add public method on `MatchingEngine` (e.g., `ModifyOrder()`)
6. Add replay case in `instrumentWorker.recover()`
7. Wire up API endpoint in `api.go`

## Middleware Chain Order

```
Request → withLogging → withCORS → authMiddleware → rateLimitMiddleware → mux → handler
```

Auth skips public paths (`/health`, `/v1/instruments`).
Rate limiter applies per-client buckets (separate read/write limits).

## Configuration Hierarchy

```
Defaults (hardcoded in config.go)
  ↓ overridden by
JSON config file (ME_CONFIG_FILE or ./config.json)
  ↓ overridden by
Environment variables (ME_* prefix)
```
