// Package api provides the HTTP REST API handlers.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/trading/matching-engine/internal/engine"
	"github.com/trading/matching-engine/internal/metrics"
	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/orderbook"
)

type apiResponse struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
func okResp(w http.ResponseWriter, data interface{}) {
	writeJSON(w, 200, apiResponse{Status: "ok", Data: data})
}
func createdResp(w http.ResponseWriter, data interface{}) {
	writeJSON(w, 201, apiResponse{Status: "ok", Data: data})
}
func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, 400, apiResponse{Status: "error", Error: msg})
}
func notFound(w http.ResponseWriter, msg string) {
	writeJSON(w, 404, apiResponse{Status: "error", Error: msg})
}
func serverError(w http.ResponseWriter, msg string) {
	writeJSON(w, 500, apiResponse{Status: "error", Error: msg})
}

// Middleware is a function that wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// NewRouter creates the API router with optional middleware layers.
// Pass nil for any middleware to skip it.
func NewRouter(me *engine.MatchingEngine, mc *metrics.Collector, authMW, rateLimitMW Middleware) http.Handler {
	mux := http.NewServeMux()
	h := &handler{me: me, mc: mc}
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/v1/orders", h.ordersHandler)
	mux.HandleFunc("/v1/orders/", h.orderByIDHandler)
	mux.HandleFunc("/v1/orderbook/", h.orderBookHandler)
	mux.HandleFunc("/v1/instruments", h.instrumentsHandler)
	mux.HandleFunc("/v1/stats", h.statsHandler)
	mux.HandleFunc("/v1/stats/", h.instrumentStatsHandler)
	mux.HandleFunc("/v1/ws", h.wsHandler)

	// Middleware chain: logging -> CORS -> auth -> rateLimit -> handler
	var chain http.Handler = mux
	if rateLimitMW != nil {
		chain = rateLimitMW(chain)
	}
	if authMW != nil {
		chain = authMW(chain)
	}
	return withLogging(withCORS(chain))
}

type handler struct {
	me *engine.MatchingEngine
	mc *metrics.Collector
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	okResp(w, map[string]string{"status": "healthy", "version": "1.0.0"})
}

func (h *handler) ordersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		badRequest(w, "only POST is supported")
		return
	}
	var req models.OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if err := validateOrderRequest(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	order, trades, err := h.me.SubmitOrder(&req)
	if err != nil {
		serverError(w, err.Error())
		return
	}
	createdResp(w, map[string]interface{}{
		"order":  orderToResponse(order),
		"trades": tradesToResponse(trades),
	})
}

func (h *handler) orderByIDHandler(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/v1/orders/")
	orderID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		badRequest(w, "invalid order ID")
		return
	}
	instrument := r.URL.Query().Get("instrument")
	if instrument == "" {
		badRequest(w, "instrument query param required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		order, err := h.me.GetOrder(instrument, orderID)
		if err != nil {
			notFound(w, err.Error())
			return
		}
		okResp(w, orderToResponse(order))
	case http.MethodDelete:
		order, err := h.me.CancelOrder(instrument, orderID)
		if err != nil {
			notFound(w, err.Error())
			return
		}
		okResp(w, orderToResponse(order))
	default:
		badRequest(w, "only GET and DELETE are supported")
	}
}

func (h *handler) orderBookHandler(w http.ResponseWriter, r *http.Request) {
	instrument := strings.TrimPrefix(r.URL.Path, "/v1/orderbook/")
	depth := 20
	if d, err := strconv.Atoi(r.URL.Query().Get("depth")); err == nil && d > 0 && d <= 100 {
		depth = d
	}
	snap, err := h.me.GetOrderBook(instrument, depth)
	if err != nil {
		notFound(w, err.Error())
		return
	}
	okResp(w, snap)
}

func (h *handler) instrumentsHandler(w http.ResponseWriter, _ *http.Request) {
	okResp(w, h.me.ListInstruments())
}

func (h *handler) statsHandler(w http.ResponseWriter, _ *http.Request) {
	okResp(w, h.mc.Snapshot())
}

func (h *handler) instrumentStatsHandler(w http.ResponseWriter, r *http.Request) {
	instrument := strings.TrimPrefix(r.URL.Path, "/v1/stats/")
	stats, err := h.me.GetBookStats(instrument)
	if err != nil {
		notFound(w, err.Error())
		return
	}
	okResp(w, stats)
}

func (h *handler) wsHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUpgradeRequired)
	_, _ = w.Write([]byte("WebSocket endpoint ready"))
}

func validateOrderRequest(req *models.OrderRequest) error {
	if req.Instrument == "" {
		return fmt.Errorf("instrument is required")
	}
	if req.Side != "BUY" && req.Side != "SELL" {
		return fmt.Errorf("side must be BUY or SELL")
	}
	valid := map[string]bool{"LIMIT": true, "MARKET": true, "IOC": true, "FOK": true}
	if !valid[req.Type] {
		return fmt.Errorf("type must be LIMIT, MARKET, IOC, or FOK")
	}
	if req.Quantity <= 0 {
		return fmt.Errorf("quantity must be positive")
	}
	if req.Type == "LIMIT" && req.Price <= 0 {
		return fmt.Errorf("price must be positive for LIMIT orders")
	}
	return nil
}

func orderToResponse(o *models.Order) map[string]interface{} {
	return map[string]interface{}{
		"id": o.ID, "client_id": o.ClientID, "instrument": o.Instrument,
		"side": o.Side.String(), "type": string(o.Type), "status": string(o.Status),
		"price": models.PriceToFloat(o.Price), "quantity": models.QtyToFloat(o.Quantity),
		"filled_qty": models.QtyToFloat(o.FilledQty), "remaining_qty": models.QtyToFloat(o.RemainingQty()),
		"avg_fill_price": models.PriceToFloat(o.AvgFillPrice),
		"created_at": o.CreatedAt, "updated_at": o.UpdatedAt,
	}
}

func tradesToResponse(results []*orderbook.MatchResult) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		t := r.Trade
		out = append(out, map[string]interface{}{
			"id": t.ID, "instrument": t.Instrument,
			"price": models.PriceToFloat(t.Price), "quantity": models.QtyToFloat(t.Quantity),
			"buy_order_id": t.BuyOrderID, "sell_order_id": t.SellOrderID,
			"buy_client_id": t.BuyClientID, "sell_client_id": t.SellClientID,
			"aggressor": t.Aggressor.String(), "timestamp": t.Timestamp,
		})
	}
	return out
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[HTTP] %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Timestamp, X-Signature")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
