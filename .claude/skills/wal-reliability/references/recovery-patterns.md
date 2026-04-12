# Recovery Patterns Reference

## State Machine: Order Lifecycle in WAL

```
                  ┌─────────────────────────────────────────┐
                  │                                         │
  EventOrderAdd   │     ┌──────────┐   match    ┌────────┐ │
  ─────────────>  │     │   NEW    │ ─────────> │ FILLED │ │
                  │     └──────────┘            └────────┘ │
                  │          │                       ▲      │
                  │          │ partial match          │      │
                  │          ▼                       │      │
                  │     ┌──────────────────┐  fill  │      │
                  │     │ PARTIALLY_FILLED │ ───────┘      │
                  │     └──────────────────┘               │
                  │          │                              │
  EventOrderCancel│          │ cancel                       │
  ─────────────>  │          ▼                              │
                  │     ┌───────────┐                       │
                  │     │ CANCELLED │                       │
                  │     └───────────┘                       │
                  │                                         │
                  │          Order Book (in-memory)          │
                  └─────────────────────────────────────────┘
```

**Important**: The WAL only records **inputs** (order add, order cancel), not **outputs** (trades, status changes). Trades are deterministically re-derived during replay by re-executing the matching logic.

## Recovery Decision Tree

```
Start Recovery
  │
  ├─ WAL disabled? → Skip recovery, start fresh
  │
  ├─ reader.HasData() == false? → No prior data, start fresh
  │
  ├─ LoadSnapshot(dir)
  │   ├─ Success with orders → RestoreOrder() each, set afterSeqNo = snapSeq
  │   ├─ Success but empty → No snapshot, replay full WAL (afterSeqNo = 0)
  │   └─ Error → Log warning, replay full WAL (afterSeqNo = 0)
  │
  └─ reader.Replay(afterSeqNo, callback)
      ├─ EventOrderAdd → DecodeOrderAdd → book.AddOrder() (with matching)
      ├─ EventOrderCancel → DecodeOrderCancel → book.CancelOrder()
      ├─ Track maxOrderID, maxTradeID across all events
      └─ On complete:
          ├─ SetMinOrderID(maxOrderID)
          ├─ SetMinTradeID(maxTradeID)
          └─ walWriter.SetSeqNo(maxSeq)
```

## Snapshot vs WAL Restore: Critical Difference

| Aspect | Snapshot Restore | WAL Replay |
|--------|-----------------|------------|
| Method | `book.RestoreOrder(order)` | `book.AddOrder(order)` |
| Matching | **No** — direct insert | **Yes** — full matching |
| Purpose | Rebuild resting order state | Re-execute order submissions |
| Orders included | Only resting (unfilled) orders | All submitted orders |
| Idempotent | Yes | No (matching changes state) |

**Why this matters**: A snapshot contains only orders that were still resting in the book at snapshot time. Fully filled orders are NOT in the snapshot. But WAL events include the original submissions, so replaying them re-executes the fills.

## File Layout on Disk

```
data/wal/
├── BTC-USD/
│   ├── wal-000001.bin     # WAL segment file
│   ├── wal-000002.bin     # Next segment
│   └── snapshot-12345.json # Snapshot at seqNo 12345
├── ETH-USD/
│   ├── wal-000001.bin
│   └── snapshot-8000.json
```

Each instrument gets its own subdirectory. This enables:
- Independent recovery per instrument
- Parallel replay across instruments
- Independent snapshot frequency per instrument (future)

## Common Recovery Failure Modes

### 1. Corrupted WAL Tail
**Cause**: Process crashed mid-write (partial event bytes)
**Symptom**: Decode error on last event
**Handling**: Replay up to last complete event, log warning, continue

### 2. Missing Snapshot File
**Cause**: Snapshot was deleted or never created
**Symptom**: `LoadSnapshot()` returns error or empty
**Handling**: Fall back to full WAL replay (slower but correct)

### 3. Stale Snapshot (older than WAL)
**Cause**: Normal operation — snapshot is always behind latest WAL
**Symptom**: WAL events exist after snapshot seqNo
**Handling**: Load snapshot, then replay WAL events after `snapSeq`

### 4. ID Collision After Recovery
**Cause**: `SetMinOrderID` / `SetMinTradeID` not called or called with wrong value
**Symptom**: New orders get IDs that already exist in the recovered book
**Handling**: Track max IDs across ALL replay events and snapshot orders

### 5. Non-Deterministic Replay
**Cause**: Matching logic changed between WAL write and replay
**Symptom**: Different order book state after recovery
**Handling**: Version the WAL format, ensure matching logic is deterministic
**Prevention**: Never use `time.Now()` or random values in matching decisions

## WAL Performance Characteristics

| Operation | Expected Latency | Bottleneck |
|-----------|-----------------|------------|
| Encode event | <100 ns | CPU (binary encoding into pre-allocated buffer) |
| Append to file | <1 us | OS write buffer |
| fdatasync | 50-500 us | Disk I/O (SSD: ~50us, HDD: ~5ms) |
| Snapshot write | 1-100 ms | Depends on order count, JSON encoding |
| Full WAL replay | 1-60s per 1M events | CPU (matching logic) |
| Snapshot load | 10-500 ms | Disk read + JSON decode |

### Reducing Recovery Time

1. **More frequent snapshots**: Reduce `snapshot_every` (e.g., 1000 instead of 10000)
   - Trade-off: More disk I/O during normal operation
2. **Parallel instrument recovery**: Already implemented — each worker recovers independently
3. **Binary snapshot format**: Replace JSON with binary encoding for faster decode
4. **WAL segment cleanup**: `CleanOldFiles()` removes segments before snapshot — less data to scan

## Adding Checksums to WAL Events

Future enhancement for tamper detection:

```go
// Proposed format with CRC32:
// [4 bytes: CRC32 of remaining bytes]
// [1 byte: event type]
// [8 bytes: sequence number]
// [... payload ...]

func AppendWithChecksum(buf []byte, data []byte) []byte {
    checksum := crc32.ChecksumIEEE(data)
    binary.BigEndian.PutUint32(buf[0:4], checksum)
    copy(buf[4:], data)
    return buf[:4+len(data)]
}

func VerifyChecksum(record []byte) ([]byte, error) {
    if len(record) < 4 {
        return nil, fmt.Errorf("record too short")
    }
    expected := binary.BigEndian.Uint32(record[0:4])
    actual := crc32.ChecksumIEEE(record[4:])
    if expected != actual {
        return nil, fmt.Errorf("checksum mismatch: expected %x, got %x", expected, actual)
    }
    return record[4:], nil
}
```
