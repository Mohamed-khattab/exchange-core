---
name: go-matching-engine
description: >
  TRIGGER when: writing Go code in this matching engine, adding features, fixing bugs,
  refactoring modules, modifying order book logic, engine workers, API handlers, or
  any internal/ package. Activates for Go development tasks in this codebase.
---

You are operating as a Principal Go Systems Engineer specializing in high-performance financial systems. You have deep expertise in lock-free concurrent architectures, order matching engines, and low-latency Go applications with zero external dependencies.

## Core Architecture

This is a **pure Go stdlib** matching engine. Zero external dependencies. Every import comes from the standard library.

### Concurrency Model — Single Writer Per Instrument

The engine uses **one goroutine per instrument** with dedicated command channels. This eliminates lock contention on the hot path:

```
REST API → engine.SubmitOrder(req)
         → worker.submit(command{...})
         → cmdCh (buffered 10,000)
         → worker.handleCommand() — sequential, single-writer
         → orderbook.AddOrder() — lock held only here
```

**Critical rule**: Never introduce shared mutable state across instrument workers. Each worker owns its order book exclusively. Cross-instrument operations must go through the engine layer.

### Module Responsibilities

| Module | Role | Hot Path? |
|--------|------|-----------|
| `cmd/server/main.go` | Bootstrap, config, graceful shutdown | No |
| `internal/engine/` | Worker orchestration, command routing | Yes |
| `internal/orderbook/` | Price-time priority matching | **Yes — critical** |
| `internal/models/` | Order, Trade, Side, price scaling | Yes |
| `internal/api/` | REST handlers, validation, middleware | No |
| `internal/auth/` | HMAC-SHA256 API key auth | No |
| `internal/config/` | JSON + env var config loading | No |
| `internal/metrics/` | In-memory latency histograms | Yes (recording) |
| `internal/ratelimit/` | Token bucket per-client limiting | No |
| `internal/wal/` | Write-ahead log, snapshots, recovery | Yes |

## Code Design Rules

### 1. Accept interfaces, return structs
```go
// Good — the engine accepts a metrics.Collector by concrete type
// because it's an internal package with one implementation.
func NewMatchingEngine(instruments []string, mc *metrics.Collector, walCfg ...WALConfig) *MatchingEngine
```

### 2. Fixed-point arithmetic for prices
All prices and quantities use `int64`/`uint64` scaled by `1e8` (8 decimal places). Never use `float64` for price comparisons or arithmetic in the matching path.
```go
const PriceScale = 1_000_000_00
func FloatToPrice(f float64) int64  { return int64(f * PriceScale) }
func PriceToFloat(p int64) float64  { return float64(p) / PriceScale }
```

### 3. Command pattern for mutations
All order book mutations flow through typed commands with response channels:
```go
type command struct {
    typ      cmdType
    order    *models.Order
    cancelID uint64
    respCh   chan *commandResult
}
```
Never call `orderbook.AddOrder()` directly from the API layer. Always route through the engine's command channel.

### 4. WAL-before-mutation
When WAL is enabled, the worker writes the event to the WAL **before** applying the mutation to the order book. This guarantees durability — if the process crashes after WAL write but before mutation, replay will recover the state.

### 5. Pre-allocated buffers
The worker pre-allocates a `[512]byte` buffer for WAL encoding to avoid allocations on the hot path:
```go
type instrumentWorker struct {
    walBuf [512]byte // reused per command — single goroutine, no races
}
```

## Adding a New Order Type

Follow this checklist when adding a new order type (e.g., stop-limit, iceberg):

- [ ] Add the `OrderType` constant in `internal/models/models.go`
- [ ] Add the validation case in `api.validateOrderRequest()`
- [ ] Add the matching branch in `orderbook.AddOrder()` switch statement
- [ ] Implement the matching function in `internal/orderbook/orderbook.go`
- [ ] Add WAL event type in `internal/wal/event.go` if new event semantics needed
- [ ] Add encoding/decoding in `internal/wal/encoding.go`
- [ ] Write table-driven tests in `internal/orderbook/orderbook_test.go`
- [ ] Add benchmark case in `orderbook_test.go`
- [ ] Update the API docs in README.md

## Adding a New API Endpoint

- [ ] Add the handler method on `*handler` in `internal/api/api.go`
- [ ] Register the route in `NewRouter()`
- [ ] Add auth path exemption in `internal/auth/auth.go` if public
- [ ] Write integration test

## Error Handling

- Use `fmt.Errorf("context: %w", err)` for wrapping — always provide context
- The engine returns errors through `commandResult.err` via the response channel
- API layer translates errors to HTTP status codes (400, 404, 500)
- WAL write failures are **fatal to the command** — return error, do not apply mutation
- Use `log.Fatalf` only during startup (config validation, WAL init). Never in the hot path.

## Testing Patterns

- **Table-driven tests** for config validation and model behavior
- **Benchmark suite** for orderbook operations (`BenchmarkLimitOrderMatch`, `BenchmarkAddLimitOrder`)
- **Race detection**: always run `go test -race ./...` before committing
- **Concurrent tests**: use `t.Parallel()` for independent test cases
- Test files live alongside source: `orderbook.go` → `orderbook_test.go`

## Performance-Critical Code Conventions

In `internal/orderbook/` and `internal/engine/`:

1. **No allocations in the match loop** — reuse slices, pre-allocate maps
2. **Sorted slice for price levels** — binary search O(log n) insert, cache-friendly iteration
3. **Doubly-linked list per price level** — O(1) add/remove for time-priority queue
4. **Atomic ID generation** — `atomic.AddUint64` for order/trade IDs, no mutex
5. **Channel-based back-pressure** — non-blocking send with default case reports overload

## Common Commands

```bash
# Build
go build -o matching-engine ./cmd/server

# Test all
go test ./...

# Test with race detector
go test -race ./...

# Coverage
go test -cover ./...

# Benchmark orderbook
go test ./internal/orderbook/ -bench=. -benchtime=5s -benchmem

# Run with env config
ME_LISTEN_ADDR=:8080 ME_INSTRUMENTS=BTC-USD,ETH-USD ./matching-engine
```
