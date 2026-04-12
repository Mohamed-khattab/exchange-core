# Matching Engine — Business & Functional Documentation

## Overview

The Matching Engine is a high-performance electronic trading system that accepts buy and sell orders for financial instruments and matches them in real time using **price-time priority**. It functions as the core of an exchange: traders submit orders through a REST API, and the engine determines which orders can be filled, at what price, and in what sequence.

The system supports multiple instruments simultaneously (e.g., BTC-USD, ETH-USD), with each instrument operating as an independent order book.

---

## Key Capabilities

| Capability | Description |
|-----------|-------------|
| Multi-instrument support | Trade any number of instruments concurrently (BTC-USD, ETH-USD, SOL-USD, etc.) |
| Four order types | LIMIT, MARKET, Immediate-or-Cancel (IOC), Fill-or-Kill (FOK) |
| Price-time priority | Fair matching: best price first, earliest order first at the same price |
| Real-time trade execution | Orders match instantly upon submission |
| Order book depth | Query the live state of bids and asks at any time |
| Order cancellation | Cancel any open (resting) order by ID |
| Crash recovery | Optional write-ahead log (WAL) preserves state across restarts |
| API key authentication | HMAC-SHA256 signed requests with read/trade permission separation |
| Per-client rate limiting | Independent rate limits for read and write operations |
| Metrics & monitoring | Live throughput, latency histograms, and per-instrument statistics |

---

## Trading Concepts

### The Order Book

An order book is a real-time ledger of outstanding buy and sell orders for a single instrument. It has two sides:

- **Bids** (buy side) — orders to purchase, ranked from highest price to lowest
- **Asks** (sell side) — orders to sell, ranked from lowest price to highest

The **spread** is the gap between the best (highest) bid and the best (lowest) ask. A tighter spread indicates deeper liquidity.

```
         Ask Side (sellers)
         ┌────────────────────────┐
         │ $50,100.00  |  30 qty  │  ← best ask
         │ $50,150.00  | 120 qty  │
         │ $50,200.00  |  45 qty  │
         └────────────────────────┘
              spread = $100.00
         ┌────────────────────────┐
         │ $50,000.00  |  80 qty  │  ← best bid
         │ $49,950.00  | 200 qty  │
         │ $49,900.00  |  55 qty  │
         └────────────────────────┘
         Bid Side (buyers)
```

### Price-Time Priority (FIFO)

When multiple orders compete to trade, the engine applies two rules in order:

1. **Price priority** — a buyer willing to pay more goes first; a seller asking less goes first
2. **Time priority** — at the same price, the order that arrived first is matched first

This is the standard matching algorithm used by major exchanges worldwide.

### Maker vs. Taker

- **Maker**: a resting order already in the book that provides liquidity
- **Taker** (aggressor): an incoming order that crosses the book and consumes liquidity

When a match occurs, the trade executes at the **maker's price**. This is standard — the resting order set the price, and the incoming order accepted it.

### VWAP (Volume-Weighted Average Price)

When a single order fills across multiple price levels, the engine tracks the volume-weighted average fill price:

```
Fill 1: 0.3 BTC at $50,000
Fill 2: 0.2 BTC at $50,100
VWAP = (0.3 * 50,000 + 0.2 * 50,100) / 0.5 = $50,040
```

This value is returned in the `avg_fill_price` field on every order.

---

## Order Types

### LIMIT Order

Places an order at a specific price. If the order can match immediately (the price crosses the book), it fills. Any remaining quantity rests in the book until filled or cancelled.

| Behavior | Detail |
|----------|--------|
| Rests in book? | Yes (unfilled portion) |
| Partial fills? | Yes |
| Price guarantee | Fills at your price or better |

**Example**: A LIMIT BUY at $50,000 will match against any resting sell orders at $50,000 or below. If 60% fills, the remaining 40% rests in the book at $50,000.

### MARKET Order

Executes immediately at the best available prices. Does not specify a price — it takes whatever liquidity is available. If there is no liquidity at all, the order is rejected.

| Behavior | Detail |
|----------|--------|
| Rests in book? | No |
| Partial fills? | Yes (remainder rejected if no liquidity) |
| Price guarantee | None — fills at market price |

**Example**: A MARKET BUY sweeps the ask side starting from the lowest ask. If the book has 100 qty and you want 150, you get 100 filled and the order status becomes PARTIALLY_FILLED.

### IOC (Immediate-or-Cancel)

Fills as much as possible immediately, then cancels any unfilled remainder. Behaves like a LIMIT order that refuses to rest in the book.

| Behavior | Detail |
|----------|--------|
| Rests in book? | No |
| Partial fills? | Yes (remainder cancelled) |
| Price guarantee | Fills at specified price or better |

**Use case**: You want to buy up to 10 BTC at $50,000 or better, but don't want an open order sitting in the book if only 6 BTC are available.

### FOK (Fill-or-Kill)

All-or-nothing. The engine checks whether the full quantity can be filled before executing. If yes, the entire order fills. If no, the entire order is cancelled — no partial fills.

| Behavior | Detail |
|----------|--------|
| Rests in book? | No |
| Partial fills? | No — all or nothing |
| Price guarantee | Fills at specified price or better |

**Use case**: You need exactly 100 ETH for a specific operation. Receiving only 80 is useless to you, so you use FOK to guarantee the full amount or nothing.

---

## Order Lifecycle

```
                     ┌───────────┐
   Submit ──────────>│    NEW    │──────────> resting in book
                     └─────┬─────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
       ┌────────────┐ ┌────────┐ ┌───────────┐
       │  PARTIALLY  │ │ FILLED │ │ CANCELLED │
       │   FILLED    │ │        │ │           │
       └──────┬──────┘ └────────┘ └───────────┘
              │
              ├────────────────────────┐
              ▼                        ▼
       ┌────────────┐          ┌───────────┐
       │   FILLED   │          │ CANCELLED │
       └────────────┘          └───────────┘
```

| Status | Meaning |
|--------|---------|
| `NEW` | Order accepted and resting in the book |
| `PARTIALLY_FILLED` | Some quantity has been filled, remainder still open |
| `FILLED` | Entire quantity has been filled |
| `CANCELLED` | Order removed from the book (by user or IOC/FOK logic) |
| `REJECTED` | Order could not be accepted (e.g., market order with no liquidity) |

---

## API Reference

### Base URL

```
http://localhost:8080    (HTTP, default)
https://localhost:8080   (HTTPS, when TLS enabled)
```

### Authentication

When authentication is enabled, all non-public endpoints require:

| Header | Required | Description |
|--------|----------|-------------|
| `X-API-Key` | Always | Your API key identifier |
| `X-Timestamp` | Write requests | Current time as Unix milliseconds |
| `X-Signature` | Write requests | HMAC-SHA256 signature (base64) |

**Signing a write request:**

```
signing_string = METHOD + "\n" + PATH + "\n" + TIMESTAMP + "\n" + BODY
signature = Base64(HMAC-SHA256(hex_decode(your_secret), signing_string))
```

Timestamps must be within **30 seconds** of the server's clock.

**Permissions:**
- `read` — required for GET requests (order book, stats, order lookup)
- `trade` — required for POST and DELETE requests (submit order, cancel order)

**Public endpoints** (no authentication required):
- `GET /health`
- `GET /v1/instruments`

---

### Endpoints

#### Health Check

```
GET /health
```

Returns server status. Always public, useful for load balancer probes.

**Response:**
```json
{
  "status": "ok",
  "data": {
    "status": "healthy",
    "version": "1.0.0"
  }
}
```

---

#### List Instruments

```
GET /v1/instruments
```

Returns all tradeable instruments. Always public.

**Response:**
```json
{
  "status": "ok",
  "data": ["BTC-USD", "ETH-USD", "SOL-USD", "BNB-USD"]
}
```

---

#### Submit Order

```
POST /v1/orders
```

Submit a new order. Returns the order details and any trades that resulted from immediate matching.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `client_id` | string | No | Your idempotency key for this order |
| `instrument` | string | Yes | Trading pair (e.g., "BTC-USD") |
| `side` | string | Yes | `"BUY"` or `"SELL"` |
| `type` | string | Yes | `"LIMIT"`, `"MARKET"`, `"IOC"`, or `"FOK"` |
| `price` | number | LIMIT only | Limit price in quote currency |
| `quantity` | number | Yes | Amount in base currency (must be > 0) |
| `stop_price` | number | No | Reserved for future stop order support |

**Example — LIMIT BUY:**
```json
{
  "client_id": "my-order-001",
  "instrument": "BTC-USD",
  "side": "BUY",
  "type": "LIMIT",
  "price": 50000.00,
  "quantity": 1.5
}
```

**Response (201 Created):**
```json
{
  "status": "ok",
  "data": {
    "order": {
      "id": 1,
      "client_id": "my-order-001",
      "instrument": "BTC-USD",
      "side": "BUY",
      "type": "LIMIT",
      "status": "NEW",
      "price": 50000.0,
      "quantity": 1.5,
      "filled_qty": 0.0,
      "remaining_qty": 1.5,
      "avg_fill_price": 0.0,
      "created_at": "2026-04-12T15:30:00Z",
      "updated_at": "2026-04-12T15:30:00Z"
    },
    "trades": []
  }
}
```

**Response with immediate match:**

If the order crosses the book and matches, the `trades` array contains the resulting fills and the order fields reflect the updated state:

```json
{
  "status": "ok",
  "data": {
    "order": {
      "id": 2,
      "client_id": "my-order-002",
      "instrument": "BTC-USD",
      "side": "BUY",
      "type": "MARKET",
      "status": "FILLED",
      "price": 0.0,
      "quantity": 0.5,
      "filled_qty": 0.5,
      "remaining_qty": 0.0,
      "avg_fill_price": 50100.0,
      "created_at": "2026-04-12T15:31:00Z",
      "updated_at": "2026-04-12T15:31:00Z"
    },
    "trades": [
      {
        "id": 1,
        "instrument": "BTC-USD",
        "price": 50100.0,
        "quantity": 0.5,
        "buy_order_id": 2,
        "sell_order_id": 1,
        "buy_client_id": "my-order-002",
        "sell_client_id": "seller-abc",
        "aggressor": "BUY",
        "timestamp": "2026-04-12T15:31:00Z"
      }
    ]
  }
}
```

**Error responses (400 Bad Request):**

| Condition | Error message |
|-----------|--------------|
| Malformed JSON | `"invalid JSON: ..."` |
| Missing instrument | `"instrument is required"` |
| Invalid side | `"side must be BUY or SELL"` |
| Invalid type | `"type must be LIMIT, MARKET, IOC, or FOK"` |
| Zero/negative quantity | `"quantity must be positive"` |
| LIMIT without price | `"price must be positive for LIMIT orders"` |
| Unknown instrument | `500: "unknown instrument: XYZ"` |

---

#### Get Order

```
GET /v1/orders/{id}?instrument=BTC-USD
```

Look up a resting order by its ID. The `instrument` query parameter is required because orders are partitioned by instrument.

**Response (200 OK):** Same order object format as above.

**Errors:**
- `400` — missing `instrument` param or invalid order ID
- `404` — order not found (may have been fully filled and removed)

---

#### Cancel Order

```
DELETE /v1/orders/{id}?instrument=BTC-USD
```

Cancel a resting order. The order is removed from the book and its status becomes `CANCELLED`.

**Response (200 OK):** The cancelled order object with `"status": "CANCELLED"`.

**Errors:**
- `400` — missing `instrument` param or invalid order ID
- `404` — order not found

---

#### Get Order Book

```
GET /v1/orderbook/{instrument}?depth=20
```

Returns a snapshot of the current order book for an instrument. Each level shows the aggregated price, total quantity, and number of orders.

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `depth` | integer | 20 | 1-100 | Number of price levels per side |

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "instrument": "BTC-USD",
    "bids": [
      { "price": 50000.00, "quantity": 3.5, "orders": 5 },
      { "price": 49990.00, "quantity": 1.2, "orders": 2 }
    ],
    "asks": [
      { "price": 50010.00, "quantity": 2.0, "orders": 3 },
      { "price": 50020.00, "quantity": 0.8, "orders": 1 }
    ],
    "timestamp": "0001-01-01T00:00:00Z",
    "sequence": 42
  }
}
```

- **Bids** are sorted descending (best bid first)
- **Asks** are sorted ascending (best ask first)
- **Sequence** is a monotonically increasing counter that changes with every book mutation

---

#### Global Metrics

```
GET /v1/stats
```

Returns system-wide metrics across all instruments.

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "uptime_seconds": 3600.5,
    "instruments": {
      "BTC-USD": {
        "orders_processed": 15000,
        "trades_executed": 7200,
        "volume_traded": 142.5,
        "cancellations": 320,
        "back_pressure": 0,
        "avg_latency_ns": 45000,
        "order_count": 15000
      },
      "ETH-USD": {
        "orders_processed": 8000,
        "trades_executed": 3100,
        "volume_traded": 890.3,
        "cancellations": 150,
        "back_pressure": 0,
        "avg_latency_ns": 38000,
        "order_count": 8000
      }
    }
  }
}
```

| Metric | Description |
|--------|-------------|
| `uptime_seconds` | Time since engine started |
| `orders_processed` | Total orders submitted to this instrument |
| `trades_executed` | Total fills (one fill = one trade) |
| `volume_traded` | Total quantity traded (in base currency) |
| `cancellations` | Total orders cancelled |
| `back_pressure` | Orders rejected because the internal queue was full |
| `avg_latency_ns` | Average order processing time in nanoseconds |

---

#### Instrument Stats

```
GET /v1/stats/{instrument}
```

Returns live order book statistics for a single instrument.

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "instrument": "BTC-USD",
    "bid_levels": 12,
    "ask_levels": 15,
    "open_orders": 340,
    "best_bid": 50000.00,
    "best_ask": 50010.00,
    "last_price": 50005.00,
    "spread": 10.00
  }
}
```

| Field | Description |
|-------|-------------|
| `bid_levels` | Number of distinct bid price levels |
| `ask_levels` | Number of distinct ask price levels |
| `open_orders` | Total resting orders in the book |
| `best_bid` | Highest bid price (0 if no bids) |
| `best_ask` | Lowest ask price (0 if no asks) |
| `last_price` | Price of the most recent trade |
| `spread` | Difference between best ask and best bid |

---

## Rate Limiting

When rate limiting is enabled, each authenticated client gets independent limits:

| Direction | Default Rate | Default Burst |
|-----------|-------------|---------------|
| Write (POST, DELETE) | 100 requests/sec | 200 |
| Read (GET) | 1,000 requests/sec | 2,000 |

When the limit is exceeded, the API returns:

```
HTTP 429 Too Many Requests
Retry-After: 1

{ "status": "error", "error": "rate limit exceeded" }
```

Wait and retry after the `Retry-After` period. Burst capacity allows short spikes above the sustained rate.

---

## Supported Instruments

Instruments are configured at startup. The default set is:

- `BTC-USD`
- `ETH-USD`
- `SOL-USD`
- `BNB-USD`

Each instrument runs as an independent order book. Orders for one instrument cannot interact with orders for another. The full list of active instruments is available at `GET /v1/instruments`.

Custom instruments can be configured via the config file or the `ME_INSTRUMENTS` environment variable (comma-separated).

---

## Price Precision

All prices and quantities support up to **8 decimal places** of precision. Internally, the engine uses fixed-point integer arithmetic to guarantee exact comparisons — no floating-point rounding errors.

```
Minimum representable price: 0.00000001
Maximum representable price: ~92,233,720,368.54775807
```

Prices in the API are sent and received as regular decimal numbers (e.g., `50000.50`). The conversion to fixed-point is handled internally.

---

## Data Durability

### Without WAL (default)

The engine runs entirely in memory. If the process stops, all order book state is lost. This is suitable for development, testing, and scenarios where state can be reconstructed from an external source.

### With WAL enabled

Every order submission and cancellation is written to a **write-ahead log** on disk before being applied. On restart, the engine automatically replays the log to restore the exact pre-shutdown state.

**Snapshots** are taken periodically (default: every 100,000 events) to speed up recovery. Instead of replaying the entire log from the beginning, the engine loads the latest snapshot and only replays events after that point.

| Scenario | Recovery behavior |
|----------|-------------------|
| Clean shutdown | Full state restored from WAL on next start |
| Crash / kill -9 | State restored up to the last synced WAL entry |
| Snapshot exists | Loads snapshot, replays only recent WAL events |
| No prior data | Starts with empty order books |

---

## Glossary

| Term | Definition |
|------|-----------|
| **Aggressor** | The incoming order that initiates a match (taker) |
| **Ask** | A sell order; the ask side of the book |
| **Back-pressure** | When the engine's internal queue is full and rejects new orders |
| **Bid** | A buy order; the bid side of the book |
| **Book** | The collection of all resting orders for one instrument |
| **Crossing** | When an incoming order's price can match against the opposite side |
| **Depth** | The number of distinct price levels in the book |
| **Fill** | A successful match between a buy and sell order, producing a trade |
| **FIFO** | First-in, first-out — the time-priority rule within a price level |
| **FOK** | Fill-or-Kill — all-or-nothing execution |
| **Instrument** | A tradeable pair (e.g., BTC-USD) |
| **IOC** | Immediate-or-Cancel — fill what's available, cancel the rest |
| **Liquidity** | The quantity of orders available to trade against |
| **Maker** | A resting order that provides liquidity |
| **Price level** | All orders at a single price point, aggregated |
| **Resting order** | An order in the book waiting to be matched |
| **Spread** | The difference between the best bid and best ask |
| **VWAP** | Volume-Weighted Average Price across multiple fills |
| **WAL** | Write-Ahead Log — durability mechanism for crash recovery |
