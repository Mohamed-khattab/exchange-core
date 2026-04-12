# Trading System Matching Engine

A high-performance, production-grade order matching engine written in Go.

## Architecture

```
cmd/server/main.go          ← entry point, HTTP server, graceful shutdown
internal/
  models/models.go          ← Order, Trade, OrderRequest data types
  orderbook/orderbook.go    ← Price-time priority order book (per instrument)
  engine/engine.go          ← Orchestrator: routes orders to instrument workers
  api/api.go                ← REST API (JSON over HTTP)
  metrics/metrics.go        ← In-memory latency histograms + counters
```

## Design Decisions

| Concern | Choice | Rationale |
|---|---|---|
| Concurrency | One goroutine per instrument | Eliminates lock contention; single writer per book |
| Data structure | Sorted slice of price levels | Simple, cache-friendly; scales to ~5k price levels |
| Price representation | Fixed-point int64 (8 decimals) | Avoids float rounding errors in financial calculations |
| Order types | LIMIT, MARKET, IOC, FOK | Covers 99% of real-world trading use cases |
| Matching priority | Price-time (FIFO per level) | Industry standard for lit markets |

## Supported Order Types

- **LIMIT** — rests in book at specified price; matches immediately if crossing
- **MARKET** — matches at best available price; rejects if no liquidity
- **IOC** (Immediate-or-Cancel) — matches what it can, cancels remainder
- **FOK** (Fill-or-Kill) — either fills completely or cancels entirely

## API Reference

### Submit Order
```http
POST /v1/orders
Content-Type: application/json

{
  "client_id":  "my-order-123",
  "instrument": "BTC-USD",
  "side":       "BUY",
  "type":       "LIMIT",
  "price":      50000.00,
  "quantity":   0.5
}
```

### Cancel Order
```http
DELETE /v1/orders/{id}?instrument=BTC-USD
```

### Get Order Book
```http
GET /v1/orderbook/BTC-USD?depth=10
```

### Get Instruments
```http
GET /v1/instruments
```

### Get Stats
```http
GET /v1/stats
GET /v1/stats/BTC-USD
```

### Health Check
```http
GET /health
```

## Building & Running

```bash
# Build
go build -o matching-engine ./cmd/server

# Run
./matching-engine

# Run tests
go test ./...

# Run benchmarks
go test ./internal/orderbook/ -bench=. -benchtime=5s -benchmem

# Docker
docker build -t matching-engine .
docker run -p 8080:8080 matching-engine
```

## Performance Targets

| Metric | Target |
|---|---|
| Order throughput | > 100,000 orders/sec per instrument |
| Matching latency (P99) | < 100 µs |
| Memory per 1M open orders | < 1 GB |
| Order book depth query | < 1 µs |

## Production Hardening Checklist

- [ ] Replace sorted slice with red-black tree (e.g., `emirpasic/gods`)
- [ ] Add Prometheus metrics exporter
- [ ] Implement WAL / event sourcing for crash recovery
- [ ] Add WebSocket feed for real-time trade/book updates
- [ ] Implement TLS + authentication middleware
- [ ] Add circuit breakers for runaway market conditions
- [ ] Add order rate limiting per client
- [ ] Implement stop-limit order activation on price triggers
- [ ] Add FIX protocol gateway adapter
