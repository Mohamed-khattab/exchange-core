// Package engine orchestrates matching across multiple instruments.
// Each instrument runs in its own goroutine with a dedicated channel,
// providing lock-free single-writer semantics per order book.
package engine

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/trading/matching-engine/internal/circuit"
	"github.com/trading/matching-engine/internal/metrics"
	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
	"github.com/trading/matching-engine/internal/wal"
)

// WALConfig holds WAL settings passed from the config layer.
type WALConfig struct {
	Enabled       bool
	Dir           string
	SyncMode      string
	SnapshotEvery int
}

// STPConfig holds self-trade prevention settings.
type STPConfig struct {
	Enabled     bool
	DefaultMode models.STPMode
}

// ── Commands ──────────────────────────────────────────────────────────────────

type cmdType int

const (
	cmdAddOrder    cmdType = iota
	cmdCancelOrder cmdType = iota
	cmdStop        cmdType = iota
)

type command struct {
	typ      cmdType
	order    *models.Order
	cancelID uint64
	respCh   chan *commandResult
}

type commandResult struct {
	order  *models.Order
	trades []*orderbook.MatchResult
	err    error
	ok     bool
}

// ── InstrumentWorker ──────────────────────────────────────────────────────────

// instrumentWorker owns a single OrderBook and processes commands sequentially.
// This eliminates lock contention: all mutations go through a single goroutine.
type instrumentWorker struct {
	instrument    string
	book          *orderbook.OrderBook
	cmdCh         chan *command
	mc            *metrics.Collector
	stopCh        chan struct{}
	done          chan struct{}
	walWriter     *wal.Writer             // nil if WAL disabled
	walBuf        [512]byte              // pre-allocated encode buffer (only used by worker goroutine)
	eventCount    int                    // events since last snapshot
	snapshotEvery int                    // 0 = no snapshots
	breaker       *circuit.InstrumentBreaker // nil if circuit breaker disabled
}

func newInstrumentWorker(instrument string, mc *metrics.Collector, walCfg WALConfig, stpCfg STPConfig, cbCfg circuit.Config) (*instrumentWorker, error) {
	book := orderbook.NewOrderBook(instrument)
	if stpCfg.Enabled {
		book.SetSTP(true, stpCfg.DefaultMode)
	}

	w := &instrumentWorker{
		instrument:    instrument,
		book:          book,
		cmdCh:         make(chan *command, 10_000),
		mc:            mc,
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
		snapshotEvery: walCfg.SnapshotEvery,
	}

	if walCfg.Enabled {
		writer, err := wal.NewWriter(walCfg.Dir, instrument, wal.ParseSyncMode(walCfg.SyncMode))
		if err != nil {
			return nil, fmt.Errorf("creating WAL writer for %s: %w", instrument, err)
		}
		w.walWriter = writer
	}

	if cbCfg.Enabled {
		w.breaker = circuit.NewInstrumentBreaker(instrument, cbCfg)
	}

	return w, nil
}

// recover replays WAL and snapshot data to restore order book state.
func (w *instrumentWorker) recover(walDir string) error {
	if w.walWriter == nil {
		return nil
	}

	reader := wal.NewReader(walDir, w.instrument)
	if !reader.HasData() {
		return nil
	}

	// Load snapshot if available
	snapshotDir := w.walWriter.Dir()
	var afterSeqNo uint64
	snapSeq, orders, err := wal.LoadSnapshot(snapshotDir)
	if err != nil {
		log.Printf("[engine] warning: failed to load snapshot for %s: %v (replaying full WAL)", w.instrument, err)
	} else if len(orders) > 0 {
		for _, o := range orders {
			w.book.RestoreOrder(o)
			if o.ID > 0 {
				models.SetMinOrderID(o.ID)
			}
		}
		afterSeqNo = snapSeq
		log.Printf("[engine] restored %d orders from snapshot (seqNo=%d) for %s", len(orders), snapSeq, w.instrument)
	}

	// Replay WAL events after the snapshot
	var replayCount int
	var maxOrderID, maxTradeID uint64

	maxSeq, err := reader.Replay(afterSeqNo, func(seqNo uint64, eventType uint8, payload []byte) error {
		switch eventType {
		case wal.EventOrderAdd, wal.EventStopActivation:
			order, err := wal.DecodeOrderAdd(payload)
			if err != nil {
				return fmt.Errorf("decoding order add: %w", err)
			}
			if order.ID > maxOrderID {
				maxOrderID = order.ID
			}
			// Re-process the order through the matching engine
			results, _ := w.book.AddOrder(order)
			// Track trade IDs from replay
			for _, r := range results {
				if r.Trade.ID > maxTradeID {
					maxTradeID = r.Trade.ID
				}
			}
			replayCount++

		case wal.EventOrderCancel:
			orderID, _, err := wal.DecodeOrderCancel(payload)
			if err != nil {
				return fmt.Errorf("decoding order cancel: %w", err)
			}
			w.book.CancelOrder(orderID)
			replayCount++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("WAL replay for %s: %w", w.instrument, err)
	}

	// Restore global counters to prevent ID collisions
	if maxOrderID > 0 {
		models.SetMinOrderID(maxOrderID)
	}
	if maxTradeID > 0 {
		models.SetMinTradeID(maxTradeID)
	}

	// Set the WAL writer's sequence number to continue from where we left off
	w.walWriter.SetSeqNo(maxSeq)

	if replayCount > 0 {
		log.Printf("[engine] replayed %d WAL events for %s (seqNo up to %d)", replayCount, w.instrument, maxSeq)
	}

	return nil
}

func (w *instrumentWorker) run() {
	defer close(w.done)
	log.Printf("[engine] worker started for %s", w.instrument)

	for {
		select {
		case cmd := <-w.cmdCh:
			w.handleCommand(cmd)
		case <-w.stopCh:
			log.Printf("[engine] worker stopping for %s", w.instrument)
			if w.walWriter != nil {
				w.walWriter.Close()
			}
			return
		}
	}
}

func (w *instrumentWorker) handleCommand(cmd *command) {
	switch cmd.typ {
	case cmdAddOrder:
		// Circuit breaker pre-check (before WAL write)
		if w.breaker != nil {
			if err := w.breaker.CheckOrder(cmd.order); err != nil {
				if cmd.respCh != nil {
					cmd.respCh <- &commandResult{err: err}
				}
				return
			}
		}

		// WAL: write BEFORE mutation
		if w.walWriter != nil {
			seq := w.walWriter.NextSeqNo()
			n := wal.EncodeOrderAdd(w.walBuf[:], seq, cmd.order)
			if err := w.walWriter.Append(w.walBuf[:n]); err != nil {
				log.Printf("[engine] WAL write error for %s: %v", w.instrument, err)
				if cmd.respCh != nil {
					cmd.respCh <- &commandResult{err: fmt.Errorf("WAL write failed: %w", err)}
				}
				return
			}
			w.eventCount++
		}

		start := time.Now()
		results, err := w.book.AddOrder(cmd.order)
		latency := time.Since(start)

		w.mc.RecordOrderProcessed(w.instrument, latency)
		for _, r := range results {
			w.mc.RecordTrade(w.instrument, r.Trade)
			log.Printf("[TRADE] %s price=%.8f qty=%.8f buy=%d sell=%d aggressor=%s",
				r.Trade.Instrument,
				models.PriceToFloat(r.Trade.Price),
				models.QtyToFloat(r.Trade.Quantity),
				r.Trade.BuyOrderID,
				r.Trade.SellOrderID,
				r.Trade.Aggressor,
			)
			// Circuit breaker post-trade check
			if w.breaker != nil {
				if w.breaker.RecordTrade(r.Trade.Price, r.Trade.Timestamp) {
					log.Printf("[CIRCUIT] trading halted for %s: %s", w.instrument, w.breaker.HaltReason())
				}
			}
		}

		// Stop order activation loop (cascade-safe, max 100 iterations)
		if len(results) > 0 {
			for cascade := 0; cascade < 100; cascade++ {
				triggered := w.book.CheckStops()
				if len(triggered) == 0 {
					break
				}
				newTrades := false
				for _, stopOrder := range triggered {
					// Convert stop to its activated type
					if stopOrder.Type == models.OrderTypeStop {
						stopOrder.Type = models.OrderTypeMarket
					} else if stopOrder.Type == models.OrderTypeStopLimit {
						stopOrder.Type = models.OrderTypeLimit
					}
					stopOrder.StopPrice = 0
					stopOrder.Status = models.StatusNew
					stopOrder.UpdatedAt = time.Now().UTC()

					// WAL: log activation
					if w.walWriter != nil {
						seq := w.walWriter.NextSeqNo()
						n := wal.EncodeStopActivation(w.walBuf[:], seq, stopOrder)
						w.walWriter.Append(w.walBuf[:n])
						w.eventCount++
					}

					// Circuit breaker check on activated order
					if w.breaker != nil {
						if err := w.breaker.CheckOrder(stopOrder); err != nil {
							stopOrder.Status = models.StatusRejected
							log.Printf("[engine] activated stop rejected by circuit breaker: %v", err)
							continue
						}
					}

					// Re-enter matching
					activationResults, _ := w.book.AddOrder(stopOrder)
					for _, r := range activationResults {
						results = append(results, r)
						w.mc.RecordTrade(w.instrument, r.Trade)
						newTrades = true
						if w.breaker != nil {
							if w.breaker.RecordTrade(r.Trade.Price, r.Trade.Timestamp) {
								log.Printf("[CIRCUIT] trading halted for %s after stop activation", w.instrument)
							}
						}
					}
				}
				if !newTrades {
					break
				}
			}
		}

		if cmd.respCh != nil {
			cmd.respCh <- &commandResult{
				order:  cmd.order,
				trades: results,
				err:    err,
			}
		}

		w.maybeSnapshot()

	case cmdCancelOrder:
		// WAL: write BEFORE mutation
		if w.walWriter != nil {
			seq := w.walWriter.NextSeqNo()
			n := wal.EncodeOrderCancel(w.walBuf[:], seq, cmd.cancelID, w.instrument)
			if err := w.walWriter.Append(w.walBuf[:n]); err != nil {
				log.Printf("[engine] WAL write error for %s: %v", w.instrument, err)
				if cmd.respCh != nil {
					cmd.respCh <- &commandResult{err: fmt.Errorf("WAL write failed: %w", err)}
				}
				return
			}
			w.eventCount++
		}

		order, ok := w.book.CancelOrder(cmd.cancelID)
		w.mc.RecordCancellation(w.instrument)
		if cmd.respCh != nil {
			cmd.respCh <- &commandResult{order: order, ok: ok}
		}

		w.maybeSnapshot()
	}
}

func (w *instrumentWorker) maybeSnapshot() {
	if w.walWriter == nil || w.snapshotEvery <= 0 {
		return
	}
	if w.eventCount >= w.snapshotEvery {
		orders := w.book.AllOrders()
		seqNo := w.walWriter.SeqNo()
		if err := wal.WriteSnapshot(w.walWriter.Dir(), seqNo, orders); err != nil {
			log.Printf("[engine] snapshot error for %s: %v", w.instrument, err)
		} else {
			log.Printf("[engine] snapshot created for %s at seqNo=%d with %d orders", w.instrument, seqNo, len(orders))
			wal.CleanOldFiles(w.walWriter.Dir(), seqNo)
		}
		w.eventCount = 0
	}
}

func (w *instrumentWorker) submit(cmd *command) {
	select {
	case w.cmdCh <- cmd:
	default:
		// Queue full — back-pressure
		if cmd.respCh != nil {
			cmd.respCh <- &commandResult{err: fmt.Errorf("order book queue full for %s", w.instrument)}
		}
		w.mc.RecordBackPressure(w.instrument)
	}
}

func (w *instrumentWorker) stop() {
	close(w.stopCh)
	<-w.done
}

// ── MatchingEngine ────────────────────────────────────────────────────────────

// EngineConfig holds all engine-level configuration.
type EngineConfig struct {
	WAL            WALConfig
	STP            STPConfig
	CircuitBreaker circuit.Config
}

// MatchingEngine manages all instrument workers and provides a unified API
// for the REST/WS layer to interact with.
type MatchingEngine struct {
	workers     map[string]*instrumentWorker
	mu          sync.RWMutex
	mc          *metrics.Collector
	tradesCh    chan *orderbook.MatchResult
	subscribers []chan *orderbook.MatchResult
	subMu       sync.Mutex
	cfg         EngineConfig
}

// NewMatchingEngine creates the engine with the given instruments.
func NewMatchingEngine(instruments []string, mc *metrics.Collector, cfgs ...EngineConfig) *MatchingEngine {
	var cfg EngineConfig
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}

	me := &MatchingEngine{
		workers:  make(map[string]*instrumentWorker, len(instruments)),
		mc:       mc,
		tradesCh: make(chan *orderbook.MatchResult, 50_000),
		cfg:      cfg,
	}
	for _, inst := range instruments {
		w, err := newInstrumentWorker(inst, mc, cfg.WAL, cfg.STP, cfg.CircuitBreaker)
		if err != nil {
			log.Fatalf("[engine] failed to create worker for %s: %v", inst, err)
		}
		me.workers[inst] = w
	}
	return me
}

// Start launches all instrument workers. If WAL is enabled, recovers state first.
func (me *MatchingEngine) Start() {
	me.mu.RLock()
	defer me.mu.RUnlock()

	// Recover state from WAL before starting workers
	if me.cfg.WAL.Enabled {
		for _, w := range me.workers {
			if err := w.recover(me.cfg.WAL.Dir); err != nil {
				log.Fatalf("[engine] recovery failed for %s: %v", w.instrument, err)
			}
		}
	}

	for _, w := range me.workers {
		go w.run()
	}
	log.Printf("[engine] started with %d instruments", len(me.workers))
}

// Stop gracefully stops all workers.
func (me *MatchingEngine) Stop() {
	me.mu.RLock()
	defer me.mu.RUnlock()
	for _, w := range me.workers {
		w.stop()
	}
	log.Println("[engine] all workers stopped")
}

// SubmitOrder routes an order to the correct instrument worker.
func (me *MatchingEngine) SubmitOrder(req *models.OrderRequest) (*models.Order, []*orderbook.MatchResult, error) {
	worker, err := me.workerFor(req.Instrument)
	if err != nil {
		return nil, nil, err
	}

	side, err := parseSide(req.Side)
	if err != nil {
		return nil, nil, err
	}
	oType := models.OrderType(req.Type)

	order := models.NewOrder(
		req.Instrument,
		side,
		oType,
		models.FloatToPrice(req.Price),
		models.FloatToPrice(req.StopPrice),
		models.FloatToQty(req.Quantity),
		req.ClientID,
	)
	if req.STPMode != "" {
		order.STPMode = models.STPMode(req.STPMode)
	}

	respCh := make(chan *commandResult, 1)
	worker.submit(&command{
		typ:    cmdAddOrder,
		order:  order,
		respCh: respCh,
	})

	result := <-respCh
	if result.err != nil {
		return nil, nil, result.err
	}
	return result.order, result.trades, nil
}

// CancelOrder sends a cancel command to the appropriate worker.
func (me *MatchingEngine) CancelOrder(instrument string, orderID uint64) (*models.Order, error) {
	worker, err := me.workerFor(instrument)
	if err != nil {
		return nil, err
	}

	respCh := make(chan *commandResult, 1)
	worker.submit(&command{
		typ:      cmdCancelOrder,
		cancelID: orderID,
		respCh:   respCh,
	})

	result := <-respCh
	if !result.ok {
		return nil, fmt.Errorf("order %d not found in %s", orderID, instrument)
	}
	return result.order, nil
}

// GetOrderBook returns a depth snapshot for an instrument.
func (me *MatchingEngine) GetOrderBook(instrument string, depth int) (*models.OrderBookSnapshot, error) {
	worker, err := me.workerFor(instrument)
	if err != nil {
		return nil, err
	}
	// Snapshot is read-only and safe to call directly
	return worker.book.Snapshot(depth), nil
}

// GetOrder looks up an order by ID.
func (me *MatchingEngine) GetOrder(instrument string, orderID uint64) (*models.Order, error) {
	worker, err := me.workerFor(instrument)
	if err != nil {
		return nil, err
	}
	order, ok := worker.book.GetOrder(orderID)
	if !ok {
		return nil, fmt.Errorf("order %d not found", orderID)
	}
	return order, nil
}

// GetBookStats returns per-instrument stats.
func (me *MatchingEngine) GetBookStats(instrument string) (map[string]interface{}, error) {
	worker, err := me.workerFor(instrument)
	if err != nil {
		return nil, err
	}
	bidLevels, askLevels, openOrders, bestBid, bestAsk, last := worker.book.Stats()
	return map[string]interface{}{
		"instrument":  instrument,
		"bid_levels":  bidLevels,
		"ask_levels":  askLevels,
		"open_orders": openOrders,
		"best_bid":    models.PriceToFloat(bestBid),
		"best_ask":    models.PriceToFloat(bestAsk),
		"last_price":  models.PriceToFloat(last),
		"spread":      models.PriceToFloat(bestAsk - bestBid),
	}, nil
}

// ListInstruments returns all active instruments.
func (me *MatchingEngine) ListInstruments() []string {
	me.mu.RLock()
	defer me.mu.RUnlock()
	out := make([]string, 0, len(me.workers))
	for k := range me.workers {
		out = append(out, k)
	}
	return out
}

// workerFor resolves a worker from an instrument symbol.
func (me *MatchingEngine) workerFor(instrument string) (*instrumentWorker, error) {
	me.mu.RLock()
	defer me.mu.RUnlock()
	w, ok := me.workers[instrument]
	if !ok {
		return nil, fmt.Errorf("unknown instrument: %s", instrument)
	}
	return w, nil
}

func parseSide(s string) (models.Side, error) {
	switch s {
	case "BUY":
		return models.SideBuy, nil
	case "SELL":
		return models.SideSell, nil
	default:
		return 0, fmt.Errorf("invalid side: %s (must be BUY or SELL)", s)
	}
}
