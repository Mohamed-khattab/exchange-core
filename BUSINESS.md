# Exchange Core - Business & Product Documentation

## Executive Summary

Exchange Core is a high-performance order matching engine designed for digital asset exchanges, traditional securities trading platforms, and fintech applications that require institutional-grade order execution. Built in Go, it delivers sub-100-microsecond matching latency with full crash recovery, regulatory compliance, and institutional connectivity.

## Product Overview

### What It Does

Exchange Core receives buy and sell orders from trading participants, matches them according to price-time priority rules, and produces trades. It manages the full order lifecycle: submission, matching, amendment, cancellation, and reporting.

### Who It's For

- **Cryptocurrency exchanges** building their own matching infrastructure
- **Traditional brokerages** requiring a customizable order execution layer
- **Fintech platforms** that need to match supply and demand (not limited to financial instruments)
- **Prop trading firms** running internal crossing networks
- **Market makers** requiring low-latency order management with self-trade prevention

### Key Differentiators

| Capability | Exchange Core | Typical Open-Source Alternatives |
|-----------|--------------|--------------------------------|
| Matching latency | <100us P99 | 1-10ms |
| Crash recovery | WAL with deterministic replay | In-memory only (data loss) |
| Order types | 6 types + stop orders | Limit/Market only |
| Auction support | Opening/closing with equilibrium price | Not available |
| Institutional connectivity | FIX 4.4 protocol | REST only |
| Replication | Primary-replica with TCP streaming | Single instance |
| Compliance | OTR monitoring, surveillance, audit trail | Manual logging |
| Self-trade prevention | 3 configurable modes | Not available |

## Feature Summary by Phase

### Phase 1: Production Foundation
- **Write-Ahead Log (WAL)**: Binary event journal with CRC32 integrity. Every order submission, cancellation, and modification is durably written before execution. On crash, the engine replays the WAL to reconstruct exact state. Configurable fsync modes balance durability vs latency.
- **TLS Encryption**: HTTPS with TLS 1.2+ for all API traffic.
- **Authentication**: API key + HMAC-SHA256 signature verification on write endpoints. 30-second timestamp window prevents replay attacks. Per-key permission control (trade vs read-only).
- **Rate Limiting**: Token bucket per client with separate read/write limits. Protects the engine from excessive load.

### Phase 2: Trading Features
- **Self-Trade Prevention (STP)**: Prevents a client from matching against their own resting orders. Three modes: cancel the resting order, cancel the incoming order, or cancel both. Configurable globally and per-order.
- **Circuit Breakers**: Automatically halts trading when price moves too fast. Price band checks reject orders far from the reference price. Velocity monitoring triggers halts on rapid movement. Fat-finger protection blocks unreasonably large orders. Auto-resume with configurable cool-down period.
- **Stop / Stop-Limit Orders**: Dormant orders that activate when a price trigger is crossed. Stop-market activates as a market order; stop-limit activates as a limit order at the specified price. Supports cascading triggers (stop triggers trade that triggers another stop, up to 100 levels deep).
- **WebSocket Real-Time Feed**: Live streaming of trades, order book updates, and ticker data. Clients subscribe per instrument and channel. Backpressure handling disconnects slow consumers.

### Phase 3: Performance & Operations
- **Red-Black Tree Price Levels**: O(log n) insert/remove for price levels, replacing the original O(n) sorted slice. Handles 50,000+ distinct price levels efficiently.
- **Prometheus Metrics**: Standard /metrics endpoint for Grafana dashboards. 12 metric types including order latency histograms, trade counters, and book depth gauges. Backward-compatible JSON stats API retained.
- **Order Amendment**: Modify price and/or quantity of resting limit orders. Quantity-decrease-only preserves time priority (in-place). Price change or quantity increase loses priority (cancel + re-insert, may produce trades).
- **Mass Cancel**: Cancel multiple orders in one operation. Filter by instrument, client ID, and/or side. Includes pending stop orders. Always allowed even during trading halts (kill switch).

### Phase 4: Scale & Institutional Access
- **Trading Sessions**: 5-phase lifecycle per instrument: PRE_OPEN, AUCTION_OPEN, CONTINUOUS, AUCTION_CLOSE, CLOSED. Orders are accepted but not matched during non-continuous phases. Circuit breaker halt overrides any session phase.
- **Auction Mechanisms**: Opening and closing auctions with equilibrium price calculation. The algorithm finds the price that maximizes matched volume, with tiebreakers by reference price and imbalance. All fills execute at the single equilibrium price. Market orders participate with highest priority.
- **Primary-Replica Replication**: Hot standby via TCP-based WAL event streaming. The primary streams events to replicas in real-time (non-blocking from matching). Replicas maintain identical state through deterministic replay. Manual failover promotes a replica to primary.
- **FIX 4.4 Protocol Gateway**: Industry-standard protocol for institutional connectivity. Supports Logon, Logout, Heartbeat, NewOrderSingle, and ExecutionReport messages. In-house parser (no external dependency). Per-session heartbeat, sequence numbers, and ClOrdID-to-OrderID mapping.

### Phase 5: Compliance & Regulatory
- **Nanosecond Timestamps**: All orders carry a ReceivedAt timestamp set at API ingress (before any processing). Trades have monotonic SequenceNo fields. All timestamps serialized as RFC3339Nano for MiFID II compliance.
- **Immutable Audit Trail**: WAL events include explicit OrderRejected (with reason codes) and TradeExecuted records. Rejection reasons: MarketClosed, CircuitBreaker, QueueFull, WALWriteFailed, InvalidOrder, OTRThrottled. Audit exporter produces structured NDJSON for compliance reporting.
- **Order-to-Trade Ratio (OTR) Monitoring**: Per-client per-instrument sliding window tracking. Configurable threshold (e.g., 100:1) with ALERT or REJECT actions. Ring buffer of 60 one-second buckets minimizes memory usage.
- **Market Surveillance**: Asynchronous detectors for manipulation patterns. Spoofing detector flags large orders cancelled within a configurable window. Layering detector flags orders at multiple price levels on the same side. Wash trading detector logs self-trade prevention triggers and flags any self-trades that bypass STP.

## Technical Specifications

### Performance

| Metric | Target |
|--------|--------|
| Order throughput | >100,000 orders/sec per instrument |
| Matching latency (P99) | <100 microseconds |
| Memory per 1M open orders | <1 GB |
| Price level operations | O(log n) |
| WAL write overhead | 10-30us (fdatasync on NVMe SSD) |

### Capacity

| Dimension | Limit |
|-----------|-------|
| Instruments | Unlimited (one goroutine each) |
| Price levels per book | 50,000+ (red-black tree) |
| Orders per instrument queue | 10,000 (buffered channel) |
| WebSocket clients | Configurable (default 1,000) |
| FIX sessions | Unlimited |
| Replication replicas | Unlimited |

### Supported Order Types

| Type | Time-in-Force | Rests in Book | Notes |
|------|--------------|---------------|-------|
| LIMIT | GTC | Yes | Standard limit order |
| MARKET | Immediate | No | Rejects if no liquidity |
| IOC | Immediate | No | Partial fill OK, remainder cancelled |
| FOK | Immediate | No | All-or-nothing |
| STOP | GTC (pending) | No (until triggered) | Activates as MARKET |
| STOP_LIMIT | GTC (pending) | No (until triggered) | Activates as LIMIT |

### API Endpoints

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| POST | /v1/orders | Write | Submit order |
| DELETE | /v1/orders | Write | Mass cancel |
| GET | /v1/orders/{id} | Read | Get order |
| PUT | /v1/orders/{id} | Write | Amend order |
| DELETE | /v1/orders/{id} | Write | Cancel order |
| GET | /v1/orderbook/{instrument} | Read | Book snapshot |
| GET | /v1/instruments | Public | List instruments |
| GET | /v1/stats | Read | JSON metrics |
| GET | /metrics | Public | Prometheus |
| GET | /health | Public | Health check |
| WS | /v1/ws | Read | Real-time feed |

### FIX 4.4 Messages

| MsgType | Tag 35 | Direction | Engine Action |
|---------|--------|-----------|---------------|
| Logon | A | Client->Server | Session establishment |
| Logout | 5 | Both | Session teardown |
| Heartbeat | 0 | Both | Keepalive |
| NewOrderSingle | D | Client->Server | engine.SubmitOrder |
| OrderCancelRequest | F | Client->Server | engine.CancelOrder |
| ExecutionReport | 8 | Server->Client | Order/fill notification |

### WAL Event Types

| Type ID | Name | Data Captured |
|---------|------|---------------|
| 1 | OrderAdd | Full order details (ID, client, instrument, side, type, price, qty) |
| 2 | OrderCancel | OrderID, instrument |
| 3 | StopActivation | Activated order details |
| 4 | OrderAmend | OrderID, new price, new qty |
| 5 | MassCancel | Filter criteria (instrument, client, side) |
| 6 | SessionTransition | From/to phase, timestamp |
| 7 | AuctionUncross | Equilibrium price, trade count |
| 8 | OrderRejected | OrderID, client, reason code, reason text |
| 9 | TradeExecuted | TradeID, both sides, price, qty, aggressor, timestamp |

### Configuration

All features are independently toggleable. The engine starts with everything disabled by default and works as a simple in-memory matching engine. Enable features incrementally as needed.

Configuration sources (in priority order):
1. Environment variables (e.g., `ME_WAL_ENABLED=true`)
2. JSON config file (path via `ME_CONFIG_FILE`)
3. Built-in defaults

### Dependencies

The engine has only 2 external Go dependencies:

| Dependency | Purpose |
|------------|---------|
| nhooyr.io/websocket | WebSocket connections |
| prometheus/client_golang | Prometheus metrics |

All other features (FIX protocol, replication, surveillance, auctions, WAL) use Go standard library only.

## Deployment

### Standalone
```bash
ME_WAL_ENABLED=true ./exchange-core
```

### With Full Security
```bash
ME_WAL_ENABLED=true ME_AUTH_ENABLED=true ME_API_KEYS_FILE=./keys.json \
ME_TLS_ENABLED=true ME_TLS_CERT=cert.pem ME_TLS_KEY=key.pem \
ME_RATE_LIMIT_ENABLED=true ME_STP_ENABLED=true \
ME_CIRCUIT_BREAKER_ENABLED=true ME_WS_ENABLED=true \
./exchange-core
```

### Primary + Replica
```bash
# Primary
ME_WAL_ENABLED=true ME_REPLICATION_ROLE=primary ME_REPLICATION_LISTEN_ADDR=:9877 ./exchange-core

# Replica
ME_WAL_ENABLED=true ME_REPLICATION_ROLE=replica ME_REPLICATION_PRIMARY_ADDR=primary:9877 ./exchange-core
```

### Docker
```bash
docker build -t exchange-core .
docker run -p 8080:8080 -v /data/wal:/data/wal -e ME_WAL_ENABLED=true exchange-core
```

## Quality

- **307 tests** across 17 packages
- **22 test files** covering matching, WAL round-trip, authentication, circuit breakers, sessions, auctions, replication, FIX parsing, OTR, and surveillance
- **go vet clean** (zero static analysis warnings)
- All mutations follow write-ahead pattern (WAL write before state change)
- CRC32 integrity on all WAL records
- Atomic snapshot writes (rename-based)
