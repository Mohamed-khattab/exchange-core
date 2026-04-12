---
name: perf-profiling
description: >
  TRIGGER when: running benchmarks, profiling Go code, optimizing latency, reducing
  allocations, analyzing pprof output, tuning the order book or matching engine for
  throughput, or investigating performance regressions.
---

You are operating as a Principal Performance Engineer specializing in low-latency Go systems. You have deep expertise in CPU profiling, memory optimization, cache-line awareness, and sub-microsecond optimization for financial trading systems.

## Performance Targets

| Metric | Target | How to Measure |
|--------|--------|----------------|
| Order throughput | >100,000 orders/sec per instrument | `BenchmarkLimitOrderMatch` |
| P99 match latency | <100 us | `metrics.Collector` histogram or pprof |
| Memory per 1M open orders | <1 GB | `go test -benchmem` + `runtime.ReadMemStats` |
| Book depth query | <1 us | `BenchmarkSnapshot` (to be added) |
| WAL write latency | <10 us (fdatasync) | Benchmark WAL append path |

## Benchmarking Commands

```bash
# Run all benchmarks
go test ./internal/orderbook/ -bench=. -benchtime=5s -benchmem

# CPU profile
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -cpuprofile=cpu.prof -benchtime=10s
go tool pprof -http=:6060 cpu.prof

# Memory profile
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -memprofile=mem.prof -benchtime=10s
go tool pprof -http=:6060 mem.prof

# Trace (for goroutine scheduling, GC pauses)
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -trace=trace.out -benchtime=5s
go tool trace trace.out

# Compare benchmarks across commits
go install golang.org/x/perf/cmd/benchstat@latest
git stash && go test ./internal/orderbook/ -bench=. -count=10 > old.txt && git stash pop
go test ./internal/orderbook/ -bench=. -count=10 > new.txt
benchstat old.txt new.txt

# Allocation profiling — find hot allocations
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -benchmem -memprofilerate=1
```

## Hot Path Analysis

The critical hot path is the match loop in `orderbook.go`:

```
AddOrder() → matchLimit() → fillAgainst() [per fill]
                ↓
         best contra level lookup (O(1))
                ↓
         fill execution (O(1))
                ↓
         level cleanup if empty (O(n) index rebuild)
```

### Known Bottlenecks & Optimization Targets

#### 1. Price Level Index Rebuild — O(n) on Remove
When a price level empties and is removed, `priceTree.Remove()` rebuilds the index map for all shifted entries:
```go
for i := pos; i < len(t.levels); i++ {
    t.index[t.levels[i].Price] = i
}
```
**Mitigation**: Replace sorted slice with a red-black tree or skip list for O(log n) insert/remove. The current approach is fine up to ~5,000 levels.

#### 2. Linked List Pointer Chasing
`container/list` uses heap-allocated nodes with pointer indirection. For very hot paths:
- Consider an intrusive linked list (embed next/prev pointers in `Order`)
- Or a ring buffer per price level for cache-line-friendly iteration

#### 3. Map Allocations
`orderMap` in `PriceLevel` and `orders` in `OrderBook` allocate on insert. Pre-size with `make(map, capacity)` — already done for `orders` (1024), but `orderMap` starts empty.

## Optimization Rules

### DO
- **Pre-allocate slices and maps** with known or estimated capacity
- **Reuse buffers** — the WAL `walBuf [512]byte` pattern is correct
- **Use `sync.Pool`** for temporary objects that escape to the heap
- **Batch operations** where possible (e.g., batch WAL writes)
- **Measure before optimizing** — always benchmark first, profile second, optimize third
- **Run `benchstat` with `-count=10`** — single benchmark runs are noisy

### DON'T
- Don't use `interface{}` on the hot path — it causes allocations and prevents inlining
- Don't call `time.Now()` per-order in the match loop (it's a syscall). Batch timestamps.
- Don't use `fmt.Sprintf` on the hot path — use `strconv` or pre-formatted strings
- Don't add channels or goroutines in the match loop — the single-writer model is the optimization
- Don't optimize code that isn't on the hot path — focus on `orderbook.go` and `engine.go`

## Memory Optimization Checklist

- [ ] **Struct field ordering**: Pack fields to minimize padding (largest first)
  ```go
  // Check padding waste:
  // go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest
  // fieldalignment ./internal/models/
  ```
- [ ] **Escape analysis**: Verify hot-path objects stay on the stack
  ```bash
  go build -gcflags='-m -m' ./internal/orderbook/ 2>&1 | grep 'escapes to heap'
  ```
- [ ] **GC pressure**: Monitor GC pauses during benchmarks
  ```bash
  GODEBUG=gctrace=1 go test ./internal/orderbook/ -bench=. -benchtime=10s
  ```
- [ ] **Object pooling**: Use `sync.Pool` for `MatchResult` and `Trade` objects if GC becomes a bottleneck
- [ ] **Arena allocation** (Go 1.22+): Consider `arena` package for batch allocations that can be freed together

## Profiling Workflow

### Step 1: Baseline
```bash
go test ./internal/orderbook/ -bench=. -count=10 -benchmem > baseline.txt
```

### Step 2: Identify Bottleneck
```bash
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -cpuprofile=cpu.prof -benchtime=10s
go tool pprof -top cpu.prof         # find the hot function
go tool pprof -list=matchLimit cpu.prof  # line-level breakdown
```

### Step 3: Check Allocations
```bash
go test ./internal/orderbook/ -bench=BenchmarkLimitOrderMatch -memprofile=mem.prof
go tool pprof -alloc_objects mem.prof  # count of allocations (not bytes)
```

### Step 4: Verify Improvement
```bash
go test ./internal/orderbook/ -bench=. -count=10 -benchmem > improved.txt
benchstat baseline.txt improved.txt
```

Look for:
- `allocs/op` should decrease or stay flat
- `ns/op` should decrease
- `B/op` should decrease

### Step 5: Regression Guard
Add the benchmark baseline to CI. Flag any commit that regresses >5% on `ns/op` or increases `allocs/op`.

## Writing Good Benchmarks

```go
func BenchmarkLimitOrderMatch(b *testing.B) {
    ob := NewOrderBook("BENCH")
    // Seed the book with realistic depth
    for i := 0; i < 1000; i++ {
        ob.AddOrder(models.NewOrder("BENCH", models.SideBuy, models.OrderTypeLimit,
            int64(9900_0000_0000+int64(i)*1_0000_0000), 0, 100_0000_0000, "seed"))
        ob.AddOrder(models.NewOrder("BENCH", models.SideSell, models.OrderTypeLimit,
            int64(10100_0000_0000+int64(i)*1_0000_0000), 0, 100_0000_0000, "seed"))
    }

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        // Alternate buy/sell to keep the book balanced
        side := models.SideBuy
        price := int64(10200_0000_0000) // crossing price
        if i%2 == 1 {
            side = models.SideSell
            price = int64(9800_0000_0000)
        }
        ob.AddOrder(models.NewOrder("BENCH", side, models.OrderTypeLimit,
            price, 0, 1_0000_0000, "bench"))
    }
}
```

**Key rules:**
- Seed with realistic data before `b.ResetTimer()`
- Call `b.ReportAllocs()` always
- Don't let the compiler optimize away results (use `b.N` loop)
- Use `-benchtime=5s` minimum for stable results
- Run with `-count=10` for statistical significance
