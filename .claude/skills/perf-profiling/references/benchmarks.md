# Benchmark Reference & Optimization Catalog

## Current Benchmarks

Located in `internal/orderbook/orderbook_test.go`:

| Benchmark | What it measures | Target |
|-----------|-----------------|--------|
| `BenchmarkLimitOrderMatch` | Full match cycle: seed 1000 orders, market-fill against them | <10 us/op |
| `BenchmarkAddLimitOrder` | Adding limit orders to book (no matching) | <1 us/op |

## Missing Benchmarks to Add

| Benchmark | Purpose |
|-----------|---------|
| `BenchmarkCancelOrder` | Cancel by ID — measures map lookup + linked list removal |
| `BenchmarkFOKMatch` | FOK liquidity check + full fill |
| `BenchmarkIOCMatch` | IOC partial fill + cancel remainder |
| `BenchmarkSnapshot` | Depth snapshot generation for REST API |
| `BenchmarkWALAppend` | WAL write latency per event |
| `BenchmarkWALReplay` | Recovery time for N events |
| `BenchmarkPriceLevelInsert` | Sorted slice insert + index rebuild |
| `BenchmarkPriceLevelRemove` | Sorted slice remove + index rebuild |
| `BenchmarkDeepBook` | Match against a book with 10k+ price levels |

## Optimization Catalog

### Completed Optimizations
- [x] Fixed-point int64 pricing (no float comparison)
- [x] Pre-allocated WAL encode buffer (512 bytes, no heap alloc)
- [x] Atomic ID generation (no mutex)
- [x] Single-writer per instrument (no lock contention)
- [x] Buffered command channel (10k, back-pressure on full)
- [x] Pre-sized order map (`make(map, 1024)`)

### Potential Optimizations (by impact)

#### High Impact
1. **Replace sorted slice with red-black tree**
   - Current: O(n) index rebuild on insert/remove
   - Improved: O(log n) insert/remove
   - When: >5,000 distinct price levels per instrument

2. **Intrusive linked list for order queue**
   - Current: `container/list` with heap-allocated nodes
   - Improved: Embed `prev`/`next` pointers directly in `Order` struct
   - Benefit: Better cache locality, fewer allocations

3. **Batch WAL writes**
   - Current: One `fdatasync` per event
   - Improved: Batch N events, single sync
   - Trade-off: Higher throughput vs. slightly higher latency per-event

#### Medium Impact
4. **Object pool for MatchResult/Trade**
   - `sync.Pool` for objects that are created per-fill and GC'd after response
   - Reduces GC pressure under high throughput

5. **Ring buffer per price level** (instead of linked list)
   - Fixed-size circular buffer for FIFO queue
   - Better cache utilization, no pointer chasing

6. **Avoid `time.Now()` per order**
   - Batch timestamp at the start of each command processing cycle
   - One syscall per command instead of per fill

#### Low Impact (diminishing returns)
7. **Field alignment in Order struct**
   - Reorder fields to minimize padding
   - Saves ~8-16 bytes per order

8. **String interning for instrument names**
   - Avoid repeated string allocations for instrument comparison

## Load Testing Script

```bash
#!/bin/bash
# Requires: curl, jq
# Sends N orders to the matching engine for throughput testing

BASE_URL="http://localhost:8080"
INSTRUMENT="BTC-USD"
N=${1:-10000}

echo "Sending $N orders to $BASE_URL..."

start=$(date +%s%N)
for i in $(seq 1 $N); do
    side="BUY"
    price="50000.00"
    if [ $((i % 2)) -eq 0 ]; then
        side="SELL"
        price="50001.00"
    fi
    curl -s -X POST "$BASE_URL/v1/orders" \
        -H "Content-Type: application/json" \
        -d "{\"client_id\":\"load-$i\",\"instrument\":\"$INSTRUMENT\",\"side\":\"$side\",\"type\":\"LIMIT\",\"price\":$price,\"quantity\":1.0}" \
        > /dev/null &

    # Batch: send 100 concurrent, then wait
    if [ $((i % 100)) -eq 0 ]; then
        wait
    fi
done
wait
end=$(date +%s%N)

elapsed=$(( (end - start) / 1000000 ))
rate=$(( N * 1000 / elapsed ))
echo "Done: $N orders in ${elapsed}ms (${rate} orders/sec)"
```

## Profiling Cheat Sheet

| What to find | Command |
|-------------|---------|
| CPU hot functions | `go tool pprof -top cpu.prof` |
| Line-level CPU | `go tool pprof -list=matchLimit cpu.prof` |
| Flamegraph | `go tool pprof -http=:6060 cpu.prof` |
| Allocation sites | `go tool pprof -alloc_objects mem.prof` |
| Allocation bytes | `go tool pprof -alloc_space mem.prof` |
| Heap in-use | `go tool pprof -inuse_space mem.prof` |
| GC pauses | `GODEBUG=gctrace=1 ./matching-engine` |
| Escape analysis | `go build -gcflags='-m' ./internal/orderbook/` |
| Inlining decisions | `go build -gcflags='-m -m' ./internal/orderbook/` |
| Goroutine scheduling | `go tool trace trace.out` |
| Mutex contention | `go test -mutexprofile=mutex.prof` |
| Block profile | `go test -blockprofile=block.prof` |
