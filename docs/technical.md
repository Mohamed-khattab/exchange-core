# Matching Engine — Technical Documentation

## Architecture

### System Overview

```
                         ┌─────────────────────────────────────────────┐
  HTTP Client            │                  Server                     │
  ───────────>           │                                             │
                         │  ┌─────────┐    ┌────────┐    ┌─────────┐  │
              Request    │  │Logging  │───>│ CORS   │───>│  Auth   │  │
             ────────>   │  │Middleware│    │Middleware│   │Middleware│  │
                         │  └─────────┘    └────────┘    └────┬────┘  │
                         │                                     │       │
                         │                               ┌─────▼─────┐ │
                         │                               │Rate Limit │ │
                         │                               │Middleware │ │
                         │                               └─────┬─────┘ │
                         │                                     │       │
                         │  ┌──────────────────────────────────▼────┐  │
                         │  │              API Handlers             │  │
                         │  │  POST /v1/orders  GET /v1/orderbook  │  │
                         │  │  DELETE /v1/orders GET /v1/stats     │  │
                         │  └──────────────────┬───────────────────┘  │
                         │                     │                       │
                         │  ┌──────────────────▼───────────────────┐  │
                         │  │          Matching Engine              │  │
                         │  │                                       │  │
                         │  │  ┌─────────────┐  ┌─────────────┐   │  │
                         │  │  │  BTC-USD    │  │  ETH-USD    │   │  │
                         │  │  │  Worker     │  │  Worker     │   │  │
                         │  │  │  (goroutine)│  │  (goroutine)│   │  │
                         │  │  │             │  │             │   │  │
                         │  │  │ ┌─────────┐ │  │ ┌─────────┐ │   │  │
                         │  │  │ │OrderBook│ │  │ │OrderBook│ │   │  │
                         │  │  │ └─────────┘ │  │ └─────────┘ │   │  │
                         │  │  │ ┌─────────┐ │  │ ┌─────────┐ │   │  │
                         │  │  │ │WAL Writer│ │  │ │WAL Writer│ │   │  │
                         │  │  │ └─────────┘ │  │ └─────────┘ │   │  │
                         │  │  └─────────────┘  └─────────────┘   │  │
                         │  └──────────────────────────────────────┘  │
                         │                                             │
                         │  ┌──────────────────────────────────────┐  │
                         │  │          Metrics Collector            │  │
                         │  └──────────────────────────────────────┘  │
                         └─────────────────────────────────────────────┘
```

### Design Principles

- **Zero external dependencies** — the entire system is built on Go's standard library
- **Single-writer per instrument** — one goroutine owns each order book, eliminating lock contention on the hot path
- **Fixed-point arithmetic** — all prices and quantities use `int64`/`uint64` scaled by 1e8, never `float64` for comparisons
- **WAL-before-mutation** — when persistence is enabled, events are written to disk before the in-memory state changes
- **Command pattern** — all order book mutations flow through typed commands with response channels

---

## Project Structure

```
matching-engine/
├── cmd/server/
│   └── main.go                  # Entry point, server bootstrap, graceful shutdown
├── internal/
│   ├── api/
│   │   └── api.go               # REST handlers, validation, middleware chain
│   ├── auth/
│   │   ├── auth.go              # HMAC-SHA256 auth middleware, permission checks
│   │   └── auth_test.go
│   ├── config/
│   │   ├── config.go            # JSON + env var config loading and validation
│   │   └── config_test.go
│   ├── engine/
│   │   └── engine.go            # Worker orchestration, command routing, WAL integration
│   ├── metrics/
│   │   ├── metrics.go           # Latency histograms, per-instrument counters
│   │   └── metrics_test.go
│   ├── models/
│   │   ├── models.go            # Order, Trade, Side, price scaling, ID generation
│   │   └── models_test.go
│   ├── orderbook/
│   │   ├── orderbook.go         # Price-time priority matching, price tree, price levels
│   │   └── orderbook_test.go
│   ├── ratelimit/
│   │   ├── limiter.go           # Token bucket algorithm, per-client registry
│   │   ├── middleware.go         # HTTP middleware for rate enforcement
│   │   └── limiter_test.go
│   └── wal/
│       ├── event.go             # Event type constants
│       ├── encoding.go          # Binary encode/decode for WAL records
│       ├── writer.go            # Append-only WAL writer with configurable sync
│       ├── reader.go            # Sequential replay reader
│       ├── snapshot.go          # Point-in-time state snapshots
│       ├── fdatasync_linux.go   # Linux fdatasync syscall
│       ├── fdatasync_other.go   # macOS/other fallback
│       └── wal_test.go
├── docs/
│   ├── business.md              # Functional / business documentation
│   └── technical.md             # This file
├── Dockerfile                   # Multi-stage Docker build
├── config.example.json          # Example configuration
├── go.mod                       # Go 1.22 module (zero dependencies)
└── README.md
```

---

## Concurrency Model

### Single-Writer Architecture

Each instrument runs in a dedicated goroutine (`instrumentWorker`). This goroutine is the **sole writer** to its order book — no other goroutine ever mutates the book directly.

```
API goroutine                     Worker goroutine (per instrument)
─────────────                     ─────────────────────────────────
engine.SubmitOrder(req)
  │
  ├─ create command{order, respCh}
  ├─ worker.submit(cmd)
  │    └─ cmdCh <- cmd             ──> select { case cmd := <-cmdCh: }
  │         (buffered 10,000)              │
  │                                        ├─ WAL write (if enabled)
  │                                        ├─ orderbook.AddOrder(order)
  │                                        ├─ metrics recording
  │                                        └─ respCh <- result
  └─ result := <-respCh           <──
```

**Why this works:**
- No mutex contention on the matching hot path
- The `sync.RWMutex` on `OrderBook` is only needed for read-only operations like `Snapshot()` and `GetOrder()` that can run concurrently with the worker
- Back-pressure: if the command channel (10k buffer) is full, `submit()` uses a non-blocking send and returns an error immediately

### Command Types

```go
cmdAddOrder    // Submit a new order (may trigger matching)
cmdCancelOrder // Cancel a resting order by ID
cmdStop        // Shut down the worker goroutine
```

Each command carries a `respCh chan *commandResult` for the API goroutine to block on until processing completes.

---

## Order Book Internals

### Data Structures

```
OrderBook
├── bids: priceTree (isBuy=true)
│   ├── levels: []*PriceLevel        ← sorted ascending by price
│   └── index: map[int64]int         ← price → slice position
├── asks: priceTree (isBuy=false)
│   ├── levels: []*PriceLevel        ← sorted ascending by price
│   └── index: map[int64]int
├── orders: map[uint64]*Order         ← all resting orders by ID
├── sequence: uint64                  ← monotonic counter (atomic)
└── lastPrice: int64                  ← last trade price

PriceLevel
├── Price: int64
├── TotalQty: uint64                  ← sum of all orders at this level
├── Orders: *list.List                ← doubly-linked list, FIFO queue
└── orderMap: map[uint64]*list.Element ← O(1) cancel by order ID
```

### Complexity

| Operation | Time | Notes |
|-----------|------|-------|
| Best price lookup | O(1) | `levels[0]` or `levels[len-1]` |
| Fill against best | O(1) | Peek front of linked list |
| Add order to level | O(1) | PushBack on linked list |
| Cancel order by ID | O(1) | Map lookup + list remove |
| Insert new price level | O(n) | Binary search + slice insert + index rebuild |
| Remove empty price level | O(n) | Slice delete + index rebuild |
| Top-N depth query | O(n) | Iterate from best, copy N levels |

The sorted slice approach is efficient for up to ~5,000 distinct price levels per side, which covers most real-world order books. For deeper books, the `priceTree` can be replaced with a balanced BST (red-black tree) for O(log n) insert/remove.

### Matching Algorithm

**LIMIT matching (`matchLimit`):**
1. Loop while the order has remaining quantity
2. Get the best contra price level (lowest ask for buy, highest bid for sell)
3. If no contra level exists or price doesn't cross — stop, rest the order
4. Execute `fillAgainst()` with the time-priority head of the contra level
5. After the loop, insert any unfilled remainder into the book

**MARKET matching (`matchMarket`):**
- Same as LIMIT but with no price constraint — sweeps all available liquidity
- Unfilled remainder is rejected (not rested)

**FOK matching (`matchFOK`):**
1. First pass: scan contra levels and sum available liquidity at crossing prices (read-only)
2. If total available < order quantity — cancel the order, return
3. If sufficient — proceed with `matchLimit()` (guaranteed full fill)

**IOC matching:**
- Execute `matchLimit()`, then cancel any unfilled remainder

### Fill Execution (`fillAgainst`)

Each fill between an aggressor and a resting order:

1. Determine fill quantity: `min(aggressor.RemainingQty, resting.RemainingQty)`
2. Fill executes at the **resting order's price** (maker price wins)
3. Update `FilledQty` on both orders
4. Update `TotalQty` on the price level
5. Update VWAP (`AvgFillPrice`) on both orders
6. Create a `Trade` record with atomic ID
7. If resting order is fully filled — remove from level, remove from `orders` map, clean empty level
8. Update statuses on both orders

---

## Write-Ahead Log (WAL)

### Record Format

All records use a binary format with CRC integrity:

```
┌──────────┬──────────┬──────────┬──────┬───────────────┐
│ Length(4) │ CRC32(4) │ SeqNo(8) │ Type │ Payload(var)  │
│ uint32   │ uint32   │ uint64   │ uint8│               │
└──────────┴──────────┴──────────┴──────┴───────────────┘
                       ◄──── CRC covers this range ────►
```

- **Length**: Total record size including the length field (4 bytes, little-endian)
- **CRC32**: IEEE CRC32 over SeqNo + Type + Payload
- **SeqNo**: Monotonically increasing sequence number
- **Type**: `1` = OrderAdd, `2` = OrderCancel
- **Payload**: Event-specific binary data

Header size is 17 bytes. Maximum record size enforced during read is 1 MB.

### Event Encoding

**OrderAdd payload:**
```
[OrderID:8][ClientID:str][Instrument:str][Side:1][Type:str]
[Price:8][StopPrice:8][Quantity:8][TimeInForce:str][CreatedAt:8]
```

**OrderCancel payload:**
```
[OrderID:8][Instrument:str]
```

Strings are length-prefixed: `[Len:2 uint16][Data:N bytes]`.

### Writer

- **Buffer**: 64 KB buffered writer (`bufio.Writer`)
- **Max file size**: 64 MB per WAL segment
- **File naming**: `wal-{6-digit-seqno}.wal` (e.g., `wal-000001.wal`)
- **Rotation**: flush + sync + close current, open new segment
- **Permissions**: files 0644, directories 0755

Each `Append()` call writes the record to the buffer, then calls the configured sync:
- `fdatasync`: flushes data to disk (not metadata) — Linux uses the raw syscall, macOS falls back to `fsync`
- `fsync`: flushes data and metadata
- `none`: no sync — OS page cache only

### Reader

`Replay(afterSeqNo, handler)` reads all `wal-*.wal` files in sorted order:

1. Open each WAL file sequentially
2. For each record: read length, read data, verify CRC, decode
3. Skip records with `seqNo <= afterSeqNo` (already covered by snapshot)
4. Call the handler for each valid record
5. Gracefully handle truncated records at EOF (crash mid-write)
6. Return the highest sequence number seen

### Snapshots

**Format**: Binary with magic number, version, CRC:

```
[Magic:4 "SNAP"][Version:1][InstrumentLen:2][Instrument:N]
[SeqNo:8][OrderCount:4][Order1][Order2]...[CRC32:4]
```

**File naming**: `snapshot-{12-digit-seqno}.snap`

**Write process** (atomic):
1. Write to temporary file (`{name}.tmp`)
2. Write all data + CRC
3. Sync the temp file
4. Rename to final name (atomic on POSIX)

**Load process**:
1. Find the latest snapshot file (alphabetically last)
2. Verify CRC before parsing
3. Decode all orders
4. Return `(seqNo, orders)`

**Cleanup**: After a successful snapshot, `CleanOldFiles()` removes older snapshots. WAL segments before the snapshot's seqNo can be safely deleted.

### Recovery Flow

```
engine.Start()
  └─ for each instrument worker:
       worker.recover(walDir)
         │
         ├─ LoadSnapshot(dir)
         │   ├─ found: RestoreOrder() each (direct insert, no matching)
         │   │         set afterSeqNo = snapshot seqNo
         │   └─ not found: afterSeqNo = 0
         │
         ├─ reader.Replay(afterSeqNo, func)
         │   ├─ EventOrderAdd: book.AddOrder() — WITH matching
         │   └─ EventOrderCancel: book.CancelOrder()
         │   track maxOrderID, maxTradeID
         │
         ├─ SetMinOrderID(maxOrderID)    ← prevent ID collisions
         ├─ SetMinTradeID(maxTradeID)
         └─ walWriter.SetSeqNo(maxSeq)   ← continue sequence
```

**Key distinction**: Snapshot restore uses `RestoreOrder()` (no matching — orders placed directly in the book). WAL replay uses `AddOrder()` (with matching — re-executes the original order flow to reconstruct trades and filled states).

---

## Authentication

### HMAC-SHA256 Signing

Write requests (POST, DELETE) require a cryptographic signature:

```
signing_string = METHOD + "\n" + PATH + "\n" + TIMESTAMP_MS + "\n" + BODY
signature = HMAC-SHA256(hex_decode(secret), signing_string)
header = Base64(signature)
```

**Headers:**
- `X-API-Key`: key identifier (looked up in config)
- `X-Timestamp`: Unix milliseconds (validated within 30-second window, both past and future)
- `X-Signature`: base64-encoded HMAC-SHA256

**Security properties:**
- Constant-time comparison via `hmac.Equal()` (no timing attacks)
- Body is read and re-wrapped so downstream handlers can still read it
- Anti-replay: 30-second timestamp window, also guards against `math.MaxInt64` overflow

### API Keys Config File

```json
{
  "keys": {
    "my-key-id": {
      "secret": "hex-encoded-256-bit-secret",
      "permissions": ["read", "trade"]
    }
  }
}
```

### Permission Model

| Permission | Grants access to |
|-----------|-----------------|
| `read` | GET requests on protected endpoints |
| `trade` | POST and DELETE requests (order submission, cancellation) |

Public paths (`/health`, `/v1/instruments`) bypass auth entirely.

---

## Rate Limiting

### Token Bucket Algorithm

Each client gets two independent token buckets — one for reads, one for writes:

```go
type TokenBucket struct {
    tokens     float64   // current tokens
    maxTokens  float64   // burst capacity
    refillRate float64   // tokens per second
    lastRefill time.Time
}
```

On each request:
1. Calculate elapsed time since last refill
2. Add `elapsed * refillRate` tokens (capped at `maxTokens`)
3. If `tokens >= 1.0` — allow request, consume one token
4. Else — reject with HTTP 429

### Client Identification

The rate limiter uses the API key ID from the auth context (`auth.ClientFromContext()`). Unauthenticated requests (public endpoints) are not rate-limited.

### Cleanup

A background goroutine runs every 5 minutes and evicts clients not seen in 10 minutes, preventing unbounded memory growth.

---

## Metrics

### Latency Histogram

8 buckets covering microsecond to second scale:

| Bucket | Range |
|--------|-------|
| 0 | < 1 us |
| 1 | < 10 us |
| 2 | < 100 us |
| 3 | < 1 ms |
| 4 | < 10 ms |
| 5 | < 100 ms |
| 6 | < 1 s |
| 7 | >= 1 s |

### Per-Instrument Counters

All counters use `atomic.AddUint64` for lock-free updates:

| Counter | Updated when |
|---------|-------------|
| `OrdersProcessed` | After each order is matched/rested |
| `TradesExecuted` | Per fill (one per trade) |
| `VolumeTraded` | Accumulated trade quantity |
| `Cancellations` | Per cancel (user-initiated or IOC/FOK) |
| `BackPressure` | When the command channel is full and rejects |

---

## Configuration

### Load Order

```
Hardcoded defaults
  └─ overridden by JSON config file
       └─ overridden by environment variables
```

The config file path is determined by:
1. `ME_CONFIG_FILE` environment variable, or
2. `./config.json` (default)

If the file doesn't exist, defaults are used (not an error).

### Complete Configuration Reference

| Field | JSON Key | Env Var | Default | Description |
|-------|----------|---------|---------|-------------|
| ListenAddr | `listen_addr` | `ME_LISTEN_ADDR` | `:8080` | Server bind address |
| Instruments | `instruments` | `ME_INSTRUMENTS` | `BTC-USD,ETH-USD,SOL-USD,BNB-USD` | Comma-separated in env |
| TLSEnabled | `tls_enabled` | `ME_TLS_ENABLED` | `false` | Enable HTTPS |
| TLSCertFile | `tls_cert_file` | `ME_TLS_CERT` | — | Path to TLS certificate |
| TLSKeyFile | `tls_key_file` | `ME_TLS_KEY` | — | Path to TLS private key |
| WALEnabled | `wal_enabled` | `ME_WAL_ENABLED` | `false` | Enable write-ahead log |
| WALDir | `wal_dir` | `ME_WAL_DIR` | `./data/wal` | WAL file directory |
| WALSyncMode | `wal_sync_mode` | `ME_WAL_SYNC_MODE` | `fdatasync` | `fdatasync`, `fsync`, or `none` |
| SnapshotEvery | `snapshot_every` | `ME_SNAPSHOT_EVERY` | `100000` | Events between snapshots |
| AuthEnabled | `auth_enabled` | `ME_AUTH_ENABLED` | `false` | Enable API key auth |
| APIKeysFile | `api_keys_file` | `ME_API_KEYS_FILE` | — | Path to API keys JSON |
| RateLimitEnabled | `rate_limit_enabled` | `ME_RATE_LIMIT_ENABLED` | `false` | Enable rate limiting |
| WriteLimitPerSec | `write_limit_per_sec` | `ME_WRITE_LIMIT_PER_SEC` | `100` | Write tokens/sec |
| ReadLimitPerSec | `read_limit_per_sec` | `ME_READ_LIMIT_PER_SEC` | `1000` | Read tokens/sec |
| WriteBurst | `write_burst` | `ME_WRITE_BURST` | `200` | Write burst capacity |
| ReadBurst | `read_burst` | `ME_READ_BURST` | `2000` | Read burst capacity |

### Validation Rules

- `listen_addr` must be non-empty
- At least one instrument is required
- TLS enabled requires both `tls_cert_file` and `tls_key_file`
- WAL `sync_mode` must be `fsync`, `fdatasync`, or `none`
- Auth enabled requires `api_keys_file`

### Environment Variable Parsing

- Booleans accept `true`, `1`, `yes` (case-insensitive)
- Numeric values must be > 0 to override
- Invalid values are silently ignored (defaults apply)

---

## HTTP Server

### Timeouts

```go
ReadTimeout:  5 * time.Second
WriteTimeout: 10 * time.Second
IdleTimeout:  60 * time.Second
```

### TLS

When enabled, the server uses TLS 1.2 minimum:

```go
TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}
```

### Middleware Chain

Requests pass through middleware in this order:

```
Request → Logging → CORS → Auth → Rate Limit → Handler → Response
```

- **Logging**: `[HTTP] METHOD PATH` for every request
- **CORS**: Allows all origins (`*`), methods GET/POST/DELETE/OPTIONS, headers Content-Type/Authorization/X-API-Key/X-Timestamp/X-Signature
- **Auth**: Validates API key, HMAC signature (write requests), and permissions. Skips public paths.
- **Rate Limit**: Per-client token bucket. Skips unauthenticated requests.

### Graceful Shutdown

On SIGINT or SIGTERM:

1. `http.Server.Shutdown()` with 10-second timeout — stops accepting new connections, waits for in-flight requests
2. Rate limit cleanup goroutine cancelled
3. `engine.Stop()` — sends stop command to each worker, waits for goroutine exit, closes WAL writers
4. Process exits

---

## Deployment

### Docker

**Build:**
```bash
docker build -t matching-engine .
```

The Dockerfile uses a multi-stage build:
- **Build stage**: `golang:1.22-alpine`, compiles static binary with `CGO_ENABLED=0`
- **Runtime stage**: `scratch` (no OS, no shell — minimal attack surface)
- **Binary flags**: `-s -w` strips debug symbols, version injected from git tags

**Run:**
```bash
docker run -p 8080:8080 \
  -v $(pwd)/config.json:/app/config.json \
  -v $(pwd)/certs:/etc/certs:ro \
  -v matching-engine-wal:/data/wal \
  -e ME_CONFIG_FILE=/app/config.json \
  matching-engine
```

**Volumes:**
- `/etc/certs` — TLS certificates (read-only)
- `/data/wal` — WAL persistence (read-write)

**Exposed port:** 8080

### Bare Metal

```bash
# Build
go build -o matching-engine ./cmd/server

# Run with defaults
./matching-engine

# Run with config
ME_CONFIG_FILE=./config.json ./matching-engine

# Run with env vars
ME_LISTEN_ADDR=:9090 \
ME_INSTRUMENTS=BTC-USD,ETH-USD \
ME_WAL_ENABLED=true \
ME_WAL_DIR=/var/data/wal \
ME_AUTH_ENABLED=true \
ME_API_KEYS_FILE=/etc/matching-engine/api_keys.json \
./matching-engine
```

---

## Testing

### Running Tests

```bash
# All tests
go test ./...

# Verbose output
go test -v ./...

# Race condition detection
go test -race ./...

# Coverage report
go test -cover ./...

# Specific package
go test -v ./internal/orderbook/
```

### Test Coverage by Package

| Package | Tests | Key areas covered |
|---------|-------|-------------------|
| `orderbook` | 22 tests + 2 benchmarks | All 4 order types, price-time priority, multi-fill, cancel, snapshot, stats, restore |
| `auth` | 10 tests | Public paths, API key validation, HMAC verification, permissions, timestamp expiry |
| `config` | 15+ tests | Defaults, JSON loading, env overrides, TLS/WAL/auth validation |
| `metrics` | 9 tests | Histogram buckets, collectors, multi-instrument, concurrency |
| `models` | 9 tests | Side, order creation, ID monotonicity, price scaling |
| `ratelimit` | 5 tests | Token bucket, registry, per-client isolation |
| `wal` | 5 tests | Event encoding/decoding, WAL write/read, recovery, snapshots |

### Benchmarks

```bash
# Order book benchmarks
go test ./internal/orderbook/ -bench=. -benchtime=5s -benchmem

# With CPU profile
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -cpuprofile=cpu.prof -benchtime=10s
go tool pprof -http=:6060 cpu.prof
```

Available benchmarks:
- `BenchmarkLimitOrderMatch` — seeds 1,000 resting orders across 100 price levels, then measures market-order matching throughput
- `BenchmarkAddLimitOrder` — measures pure insertion (no matching) across 500 price levels

---

## ID Generation

Order IDs and trade IDs are globally unique, monotonically increasing `uint64` values generated via `atomic.AddUint64`:

```go
var globalOrderID uint64
func NextOrderID() uint64 { return atomic.AddUint64(&globalOrderID, 1) }
```

On recovery, `SetMinOrderID()` and `SetMinTradeID()` use a CAS loop to ensure the counter is at least as high as the maximum recovered ID:

```go
func SetMinOrderID(id uint64) {
    for {
        old := atomic.LoadUint64(&globalOrderID)
        if id <= old { return }
        if atomic.CompareAndSwapUint64(&globalOrderID, old, id) { return }
    }
}
```

This prevents ID collisions between recovered and newly created orders.

---

## Price Representation

All prices use fixed-point `int64` scaled by 1e8. All quantities use `uint64` scaled by 1e8.

```
$50,000.50  →  5_000_050_000_000 (int64)
1.5 BTC     →    150_000_000     (uint64)
0.00000001  →              1     (minimum unit)
```

Conversion functions:
```go
FloatToPrice(50000.50) → 5000050000000
PriceToFloat(5000050000000) → 50000.50
FloatToQty(1.5) → 150000000
QtyToFloat(150000000) → 1.5
```

The API accepts and returns human-readable floats. The conversion is transparent to clients.

---

## Logging

The engine uses Go's standard `log` package with short-file format:

```
2026/04/12 15:30:00 main.go:45: [BOOT] matching engine v1.0.0
```

### Log Prefixes

| Prefix | Source | Meaning |
|--------|--------|---------|
| `[BOOT]` | main.go | Startup messages |
| `[SHUTDOWN]` | main.go | Graceful shutdown progress |
| `[FATAL]` | main.go | Fatal startup errors |
| `[HTTP]` | api.go | Every HTTP request (method + path) |
| `[TRADE]` | engine.go | Every trade execution (instrument, price, qty, order IDs) |
| `[engine]` | engine.go | Worker lifecycle, WAL events, recovery, snapshots |

### Trade Log Format

```
[TRADE] BTC-USD price=50000.50000000 qty=1.50000000 buy=123 sell=456 aggressor=BUY
```

---

## Performance Characteristics

### Targets

| Metric | Target |
|--------|--------|
| Order throughput | >100,000 orders/sec per instrument |
| P99 match latency | <100 us |
| Memory per 1M open orders | <1 GB |
| Book depth query | <1 us |

### Hot Path

The performance-critical path is:

```
worker.handleCommand → WAL Append → orderbook.AddOrder → matchLimit → fillAgainst
```

Optimizations on this path:
- Pre-allocated 512-byte WAL encode buffer (no heap allocation)
- Atomic ID generation (no mutex)
- Sorted slice with O(1) best-price lookup
- Linked list with O(1) time-priority dequeue
- Single-writer goroutine (no lock contention)

### What Is NOT on the Hot Path

- Config loading (startup only)
- Auth middleware (before engine, not in matching loop)
- Rate limiting (before engine)
- Snapshot writes (periodic, not per-order)
- HTTP JSON encoding (after matching completes)
