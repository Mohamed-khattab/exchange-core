# Exchange Core

A high-performance, institutional-grade order matching engine written in Go. Features price-time priority matching, WAL persistence with crash recovery, real-time WebSocket feeds, FIX 4.4 protocol gateway, auction mechanisms, primary-replica replication, and regulatory compliance tooling.

**307 tests | 17 packages | 39 source files | Zero mandatory external dependencies**

## Architecture

```
cmd/server/main.go                 ← Entry point, config, TLS, graceful shutdown

internal/
  models/models.go                 ← Order, Trade, Side, OrderType, price scaling (int64 x 1e8)
  orderbook/
    orderbook.go                   ← Price-time priority order book per instrument
    rbtree.go                      ← Red-black tree for O(log n) price level operations
    stopbook.go                    ← Pending stop order storage + trigger detection
  engine/engine.go                 ← Orchestrator: one goroutine per instrument, command pipeline
  api/api.go                       ← REST API (JSON/HTTP), Prometheus /metrics
  config/config.go                 ← Env vars + JSON config, all features toggleable
  metrics/metrics.go               ← Dual-write: in-memory + Prometheus counters/histograms
  wal/                             ← Write-ahead log: binary encoding, per-instrument files, snapshots
  auth/auth.go                     ← API key + HMAC-SHA256 signature verification
  ratelimit/                       ← Token bucket per client (separate read/write limits)
  circuit/breaker.go               ← Circuit breaker: price bands, velocity monitoring, fat-finger
  session/session.go               ← Trading session phases (PRE_OPEN → AUCTION → CONTINUOUS → CLOSED)
  auction/equilibrium.go           ← Equilibrium price calculation for opening/closing auctions
  ws/                              ← WebSocket hub: subscribe per instrument/channel, backpressure
  replication/                     ← Primary-replica WAL streaming over TCP
  fix/                             ← FIX 4.4 gateway: parser, session management, order handlers
  compliance/otr.go                ← Order-to-trade ratio monitoring (sliding window per client)
  surveillance/                    ← Spoofing, layering, wash trading detectors (async)
  audit/exporter.go                ← WAL-based compliance audit trail (JSON/CSV export)
```

## Design Decisions

| Concern | Choice | Rationale |
|---|---|---|
| Concurrency | One goroutine per instrument | Eliminates lock contention; single writer per book |
| Price levels | Red-black tree | O(log n) insert/remove; scales beyond 50k levels |
| Price representation | Fixed-point int64 (8 decimals) | Avoids float rounding errors in financial calculations |
| Matching priority | Price-time (FIFO per level) | Industry standard for lit markets |
| Persistence | Write-ahead log (binary, per-instrument) | Crash recovery via deterministic replay |
| Timestamps | time.Time (nanosecond) + UnixNano WAL encoding | MiFID II compliant monotonic sequence |
| Metrics | Dual-write: in-memory atomics + Prometheus | /v1/stats backward compat + /metrics for Grafana |
| FIX protocol | In-house minimal parser | 5 message types, no external dependency |
| Surveillance | Async detectors via buffered channel | Zero matching latency impact |

## Supported Order Types

| Type | Behavior |
|------|----------|
| **LIMIT** | Rests in book at specified price; matches immediately if crossing |
| **MARKET** | Matches at best available price; rejects if no liquidity |
| **IOC** | Immediate-or-Cancel: matches what it can, cancels remainder |
| **FOK** | Fill-or-Kill: fills completely or cancels entirely |
| **STOP** | Stop-market: activates as MARKET when lastPrice crosses stopPrice |
| **STOP_LIMIT** | Stop-limit: activates as LIMIT at price when lastPrice crosses stopPrice |

## Features

### Core Matching
- Price-time priority (FIFO) with red-black tree price levels
- Self-trade prevention (STP): CANCEL_RESTING, CANCEL_INCOMING, CANCEL_BOTH modes
- Order amendment: in-place qty decrease (preserves priority), cancel+re-insert for price change
- Mass cancel: filter by instrument, client ID, and/or side (includes stop orders)
- Stop order activation with cascade support (max 100 iterations)

### Persistence & Recovery
- Binary WAL with CRC32 integrity, per-instrument files, configurable fsync
- 9 event types: OrderAdd, OrderCancel, StopActivation, OrderAmend, MassCancel, SessionTransition, AuctionUncross, OrderRejected, TradeExecuted
- Periodic snapshots with atomic rename; old WAL cleanup
- Full crash recovery: snapshot + WAL replay restores exact state

### Security & Access Control
- TLS 1.2+ with configurable cert/key paths
- API key authentication with HMAC-SHA256 signature verification for write endpoints
- 30-second timestamp window prevents replay attacks
- Per-key permission control (trade, read)
- Token bucket rate limiting: separate read/write limits per client

### Trading Sessions & Auctions
- 5-phase session lifecycle: PRE_OPEN, AUCTION_OPEN, CONTINUOUS, AUCTION_CLOSE, CLOSED
- Opening/closing auctions with equilibrium price calculation (maximizes matched volume)
- Uncross: all fills at single equilibrium price; market orders fill first
- Indicative price publication via WebSocket during auction phases
- Circuit breaker composes with sessions: halt overrides any phase

### Real-Time Data
- WebSocket feed: subscribe per instrument and channel (trades, book, ticker)
- Per-client buffered channels (256) with backpressure (drop + disconnect slow clients)
- Prometheus metrics: 12 metric types at /metrics endpoint
- JSON stats at /v1/stats (backward compatible)

### Institutional Connectivity
- FIX 4.4 gateway: Logon, Logout, Heartbeat, NewOrderSingle, ExecutionReport
- In-house tag=value parser with checksum validation
- Per-session heartbeat, sequence numbers, ClOrdID mapping
- Primary-replica replication via TCP WAL streaming
- Replica failover: promote endpoint switches to primary mode

### Compliance & Regulatory
- Nanosecond timestamps on all orders and trades (MiFID II compliant)
- ReceivedAt field captured at API ingress for latency tracking
- Monotonic trade sequence numbers
- Immutable audit trail: WAL with EventOrderRejected (reason codes) and EventTradeExecuted
- Audit exporter: structured NDJSON output from WAL events
- Order-to-trade ratio monitoring: per-client sliding window with ALERT/REJECT actions
- Market surveillance: async spoofing, layering, and wash trading detectors

### Safety Mechanisms
- Circuit breakers: price band checks, velocity monitoring, fat-finger protection
- Auto-resume with configurable pre-open duration
- Trading halt overrides all session phases
- Mass cancel always allowed during halt (kill switch)
- Stop cascade safety limit (100 iterations)

## API Reference

### Orders
```http
POST   /v1/orders                              Submit order
DELETE /v1/orders?instrument=X&client_id=Y      Mass cancel (filter by instrument/client/side)
GET    /v1/orders/{id}?instrument=X             Get order details
PUT    /v1/orders/{id}?instrument=X             Amend order (price/quantity)
DELETE /v1/orders/{id}?instrument=X             Cancel order
```

### Market Data
```http
GET /v1/orderbook/{instrument}?depth=10         Order book snapshot
GET /v1/instruments                              List instruments
GET /v1/stats                                    Global metrics (JSON)
GET /v1/stats/{instrument}                       Per-instrument stats
GET /metrics                                     Prometheus metrics
```

### WebSocket
```
WS /v1/ws
→ {"type":"subscribe","channels":["trades","book","ticker"],"instruments":["BTC-USD"]}
← {"type":"trade","instrument":"BTC-USD","data":{...}}
```

### System
```http
GET  /health                                     Health check
```

### Submit Order Example
```json
{
  "client_id":  "my-order-123",
  "instrument": "BTC-USD",
  "side":       "BUY",
  "type":       "LIMIT",
  "price":      50000.00,
  "quantity":   0.5,
  "stop_price": 0,
  "stp_mode":   "CANCEL_RESTING"
}
```

### Amend Order Example
```http
PUT /v1/orders/42?instrument=BTC-USD
```
```json
{
  "price":    51000.00,
  "quantity": 0.3
}
```

## Configuration

All features are independently toggleable via environment variables or JSON config file (`ME_CONFIG_FILE`).

| Env Var | Default | Description |
|---------|---------|-------------|
| `ME_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `ME_INSTRUMENTS` | `BTC-USD,ETH-USD,SOL-USD,BNB-USD` | Comma-separated instruments |
| `ME_TLS_ENABLED` | `false` | Enable HTTPS |
| `ME_TLS_CERT` | | TLS certificate path |
| `ME_TLS_KEY` | | TLS private key path |
| `ME_WAL_ENABLED` | `false` | Enable write-ahead log |
| `ME_WAL_DIR` | `./data/wal` | WAL directory |
| `ME_WAL_SYNC_MODE` | `fdatasync` | `fsync`, `fdatasync`, or `none` |
| `ME_AUTH_ENABLED` | `false` | Enable API key auth |
| `ME_API_KEYS_FILE` | | Path to API keys JSON |
| `ME_RATE_LIMIT_ENABLED` | `false` | Enable rate limiting |
| `ME_STP_ENABLED` | `false` | Enable self-trade prevention |
| `ME_STP_DEFAULT_MODE` | | `CANCEL_RESTING`, `CANCEL_INCOMING`, or `CANCEL_BOTH` |
| `ME_CIRCUIT_BREAKER_ENABLED` | `false` | Enable circuit breakers |
| `ME_PRICE_BAND_PCT` | `0.05` | Price band (5%) |
| `ME_VELOCITY_PCT` | `0.10` | Velocity trigger (10%) |
| `ME_WS_ENABLED` | `false` | Enable WebSocket feed |
| `ME_SESSION_ENABLED` | `false` | Enable trading sessions |

## Building & Running

```bash
# Build
go build -o exchange-core ./cmd/server

# Run (all features disabled by default)
./exchange-core

# Run with persistence + auth
ME_WAL_ENABLED=true ME_AUTH_ENABLED=true ME_API_KEYS_FILE=./api_keys.json ./exchange-core

# Run with everything
ME_WAL_ENABLED=true ME_AUTH_ENABLED=true ME_API_KEYS_FILE=./api_keys.json \
ME_STP_ENABLED=true ME_CIRCUIT_BREAKER_ENABLED=true ME_WS_ENABLED=true \
ME_RATE_LIMIT_ENABLED=true ME_TLS_ENABLED=true ME_TLS_CERT=cert.pem ME_TLS_KEY=key.pem \
./exchange-core

# Tests
go test ./...

# Benchmarks
go test ./internal/orderbook/ -bench=. -benchtime=5s -benchmem

# Docker
docker build -t exchange-core .
docker run -p 8080:8080 exchange-core
```

## Performance Targets

| Metric | Target |
|---|---|
| Order throughput | > 100,000 orders/sec per instrument |
| Matching latency (P99) | < 100 us |
| Memory per 1M open orders | < 1 GB |
| Price level operations | O(log n) via red-black tree |

## Dependencies

| Dependency | Version | Purpose |
|------------|---------|---------|
| `nhooyr.io/websocket` | v1.8.17 | WebSocket connections |
| `prometheus/client_golang` | v1.23.2 | Prometheus metrics |

All other features (FIX parser, replication protocol, surveillance, auction) are implemented using Go standard library only.

## Test Suite

307 tests across 17 packages. Run with `go test ./...`.

| Package | Tests | Coverage |
|---------|-------|----------|
| api | 35 | 75.6% |
| auction | 10 | 98.2% |
| auth | 15 | 91.8% |
| circuit | 19 | 95.9% |
| compliance | 11 | 80.2% |
| config | 16 | 78.6% |
| engine | 18 | 48.6% |
| fix | 11 | 22.3% |
| metrics | 10 | 91.9% |
| models | 14 | 84.8% |
| orderbook | 45 | 82.5% |
| ratelimit | 11 | 97.9% |
| replication | 7 | 78.9% |
| session | 12 | 95.1% |
| surveillance | 12 | 87.9% |
| wal | 16 | 60.2% |
| ws | 13 | 86.8% |
