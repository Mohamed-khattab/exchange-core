---
name: wal-reliability
description: >
  TRIGGER when: working on WAL (write-ahead log), crash recovery, snapshots, data
  durability, event encoding/decoding, fdatasync, or testing recovery scenarios.
  Activates for persistence and reliability work.
---

You are operating as a Principal Reliability Engineer specializing in durable storage systems, write-ahead logs, and crash recovery for financial systems where data loss means monetary loss.

## WAL Architecture Overview

The WAL provides crash-recovery durability for the matching engine. Every state mutation is persisted to disk **before** being applied to the in-memory order book.

### Components

| File | Responsibility |
|------|---------------|
| `internal/wal/event.go` | Event type constants (`EventOrderAdd=1`, `EventOrderCancel=2`) |
| `internal/wal/encoding.go` | Binary encode/decode for events |
| `internal/wal/writer.go` | Append-only WAL file writer with configurable sync |
| `internal/wal/reader.go` | Sequential WAL file reader for replay |
| `internal/wal/snapshot.go` | Point-in-time state snapshots |
| `internal/wal/fdatasync_linux.go` | Linux-specific fdatasync syscall |
| `internal/wal/fdatasync_other.go` | Fallback for non-Linux platforms |

### Write Path

```
engine.handleCommand(cmd)
  │
  ├─ 1. walWriter.NextSeqNo()          // monotonic sequence number
  ├─ 2. EncodeOrderAdd(walBuf, seq, order)  // encode into pre-allocated buffer
  ├─ 3. walWriter.Append(walBuf[:n])    // write + sync to disk
  │     └─ fdatasync / fsync / none     // configurable durability
  ├─ 4. book.AddOrder(order)            // apply mutation AFTER WAL write
  ├─ 5. eventCount++
  └─ 6. maybeSnapshot()                 // periodic full-state snapshot
```

**Critical invariant**: Step 3 (WAL write) MUST succeed before step 4 (mutation). If WAL write fails, the command returns an error and the order book is NOT modified.

### Recovery Path

```
engine.Start()
  └─ worker.recover(walDir)
       │
       ├─ 1. wal.LoadSnapshot(dir) → (snapSeq, orders)
       │     └─ RestoreOrder() for each — direct insert, NO matching
       │
       ├─ 2. reader.Replay(afterSeqNo, callback)
       │     ├─ EventOrderAdd → book.AddOrder() — WITH matching
       │     └─ EventOrderCancel → book.CancelOrder()
       │
       ├─ 3. SetMinOrderID(maxOrderID)   // prevent ID collisions
       ├─ 4. SetMinTradeID(maxTradeID)
       └─ 5. walWriter.SetSeqNo(maxSeq)  // continue from last sequence
```

**Key distinction**: Snapshot restore uses `RestoreOrder()` (no matching), but WAL replay uses `AddOrder()` (with matching). This is because the snapshot captures resting orders, while WAL events represent the original order submissions that need re-matching.

## Sync Modes

| Mode | Durability | Throughput | Use Case |
|------|-----------|------------|----------|
| `fdatasync` | Data durable, metadata may lag | High | **Default** — production recommended |
| `fsync` | Data + metadata durable | Medium | Maximum safety, regulatory requirement |
| `none` | OS page cache only | Highest | Development, testing, volatile data |

**fdatasync vs fsync**: `fdatasync` only flushes data to disk, not metadata (file size, timestamps). This is sufficient for append-only WAL files where metadata loss on crash is acceptable — the file size will be corrected on next write.

### Platform Behavior

- **Linux**: Uses `fdatasync(2)` syscall directly — optimal
- **macOS/other**: Falls back to `fsync(2)` — slightly slower but correct
- **Windows**: Not currently supported (no syscall binding)

## Binary Encoding Format

WAL events are encoded in a compact binary format:

```
OrderAdd event:
  [1 byte: event type (1)]
  [8 bytes: sequence number (uint64 big-endian)]
  [8 bytes: order ID (uint64)]
  [N bytes: instrument (length-prefixed string)]
  [1 byte: side (1=buy, 0xFF=sell)]
  [N bytes: order type (length-prefixed string)]
  [8 bytes: price (int64)]
  [8 bytes: stop price (int64)]
  [8 bytes: quantity (uint64)]
  [N bytes: client ID (length-prefixed string)]

OrderCancel event:
  [1 byte: event type (2)]
  [8 bytes: sequence number (uint64)]
  [8 bytes: order ID (uint64)]
  [N bytes: instrument (length-prefixed string)]
```

The pre-allocated `walBuf [512]byte` in the worker avoids heap allocations. This buffer is safe because only one goroutine (the worker) uses it.

## Snapshot Format

Snapshots capture all resting orders in the book at a point in time. They are JSON-encoded for debuggability:

```json
{
  "seq_no": 12345,
  "orders": [
    {"id": 1, "instrument": "BTC-USD", "side": 1, "price": 5000000000000, ...},
    ...
  ]
}
```

### Snapshot Lifecycle

1. **Trigger**: `eventCount >= snapshotEvery` (configurable)
2. **Create**: `wal.WriteSnapshot(dir, seqNo, orders)` — writes atomic temp file, then rename
3. **Clean**: `wal.CleanOldFiles(dir, seqNo)` — removes WAL segments before snapshot
4. **Reset**: `eventCount = 0`

## Configuration

```json
{
  "wal": {
    "enabled": true,
    "dir": "./data/wal",
    "sync_mode": "fdatasync",
    "snapshot_every": 10000
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `enabled` | `false` | Enable/disable WAL |
| `dir` | `./data/wal` | WAL file directory |
| `sync_mode` | `fdatasync` | Sync mode: `fdatasync`, `fsync`, `none` |
| `snapshot_every` | `10000` | Events between snapshots (0 = no snapshots) |

## Testing Recovery Scenarios

### Test 1: Clean Recovery (WAL only, no snapshot)
```go
func TestRecoveryFromWAL(t *testing.T) {
    // 1. Create engine with WAL enabled
    // 2. Submit N orders, observe trades
    // 3. Stop engine (simulates clean shutdown)
    // 4. Create new engine pointing to same WAL dir
    // 5. Start engine — triggers recovery
    // 6. Verify: order book state matches pre-shutdown state
    // 7. Verify: new order IDs don't collide with recovered IDs
}
```

### Test 2: Recovery with Snapshot
```go
func TestRecoveryFromSnapshot(t *testing.T) {
    // 1. Create engine with WAL + snapshots (snapshot_every=100)
    // 2. Submit 500 orders (triggers multiple snapshots)
    // 3. Stop engine
    // 4. Create new engine, start — should load snapshot + replay remaining WAL
    // 5. Verify: state identical to pre-shutdown
    // 6. Verify: snapshot file exists with expected seqNo
}
```

### Test 3: Crash Mid-Write (Truncated WAL)
```go
func TestRecoveryFromTruncatedWAL(t *testing.T) {
    // 1. Write N complete events to WAL
    // 2. Append partial/corrupted bytes (simulate crash mid-write)
    // 3. Recovery should replay N events and stop at corruption
    // 4. Verify: no panic, state is consistent up to last complete event
}
```

### Test 4: ID Continuity After Recovery
```go
func TestIDContinuityAfterRecovery(t *testing.T) {
    // 1. Submit orders, record max order ID and trade ID
    // 2. Stop and recover
    // 3. Submit new order — ID must be > max recovered ID
    // 4. Verify no ID reuse
}
```

### Test 5: Empty WAL Recovery
```go
func TestRecoveryEmptyWAL(t *testing.T) {
    // 1. Create engine with WAL enabled but no prior data
    // 2. Start engine — should handle gracefully (no crash, empty book)
}
```

## Reliability Rules

### 1. WAL Write Failure = Command Failure
If `walWriter.Append()` returns an error, the command MUST fail. Never apply the mutation to the order book if the WAL write failed. Data loss is worse than a rejected order.

### 2. Replay Must Be Deterministic
WAL replay must produce the exact same order book state as the original execution. This means:
- Order IDs must be preserved (encoded in WAL events)
- Matching logic must be deterministic (no random components)
- Time-dependent behavior (e.g., expiry) must use event timestamps, not wall clock

### 3. Snapshots Must Be Atomic
Snapshot writes must use the write-to-temp-then-rename pattern. A partially written snapshot file would corrupt recovery.

### 4. Sequence Numbers Must Be Monotonic
The WAL sequence number must strictly increase. On recovery, the writer must resume from `maxSeq + 1` to prevent sequence gaps or reuse.

### 5. Old WAL Cleanup Must Be Conservative
Only delete WAL files whose events are fully covered by a valid snapshot. If snapshot integrity is uncertain, keep the WAL files.

## Adding a New WAL Event Type

1. Add event constant in `internal/wal/event.go`:
   ```go
   const EventOrderModify uint8 = 3
   ```

2. Add encode function in `internal/wal/encoding.go`:
   ```go
   func EncodeOrderModify(buf []byte, seqNo uint64, orderID uint64, newPrice int64, newQty uint64) int
   ```

3. Add decode function:
   ```go
   func DecodeOrderModify(payload []byte) (orderID uint64, newPrice int64, newQty uint64, err error)
   ```

4. Add replay case in `engine.go` `recover()`:
   ```go
   case wal.EventOrderModify:
       orderID, newPrice, newQty, err := wal.DecodeOrderModify(payload)
       // apply modification to order book
   ```

5. Add WAL write in `handleCommand()` before mutation

6. Write round-trip test: encode → decode → verify fields match

## Monitoring & Alerting

| Metric | Alert Condition | Action |
|--------|----------------|--------|
| WAL write latency | P99 > 1ms | Check disk I/O, consider faster storage |
| WAL file size | > 1GB per instrument | Reduce `snapshot_every` or increase snapshot frequency |
| Recovery time | > 30s per instrument | More frequent snapshots needed |
| WAL write errors | Any occurrence | Investigate disk health, check permissions |
| Snapshot failures | Any occurrence | Check disk space, file permissions |
