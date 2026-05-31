package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/trading/matching-engine/internal/api"
	"github.com/trading/matching-engine/internal/engine"
	"github.com/trading/matching-engine/internal/metrics"
)

func newTestRouter() http.Handler {
	mc := metrics.NewCollector()
	me := engine.NewMatchingEngine([]string{"BTC-USD", "ETH-USD"}, mc)
	me.Start()
	return api.NewRouter(me, mc, nil, nil)
}

type apiResp struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

func decode(t *testing.T, body *bytes.Buffer) apiResp {
	t.Helper()
	var resp apiResp
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// ── Health ───────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	resp := decode(t, rec.Body)
	if resp.Status != "ok" {
		t.Errorf("status = %s", resp.Status)
	}
}

// ── Instruments ──────────────────────────────────────────────────────────────

func TestListInstruments(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/instruments", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	resp := decode(t, rec.Body)
	var instruments []string
	json.Unmarshal(resp.Data, &instruments)
	if len(instruments) != 2 {
		t.Errorf("expected 2 instruments, got %d", len(instruments))
	}
}

// ── Submit Order ─────────────────────────────────────────────────────────────

func TestSubmitLimitOrder(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body)
	if resp.Status != "ok" {
		t.Errorf("resp status = %s, error = %s", resp.Status, resp.Error)
	}
}

func TestSubmitOrderInvalidJSON(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitOrderMissingInstrument(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","side":"BUY","type":"LIMIT","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitOrderInvalidSide(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","instrument":"BTC-USD","side":"HOLD","type":"LIMIT","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitOrderInvalidType(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","instrument":"BTC-USD","side":"BUY","type":"STOP","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitOrderZeroQuantity(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitLimitOrderZeroPrice(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":0,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitOrderWrongMethod(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/orders", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitOrderUnknownInstrument(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t1","instrument":"DOGE-USD","side":"BUY","type":"LIMIT","price":1,"quantity":100}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 500 {
		t.Errorf("status = %d", rec.Code)
	}
}

// ── Order By ID ──────────────────────────────────────────────────────────────

func submitTestOrder(t *testing.T, router http.Handler) uint64 {
	t.Helper()
	body := `{"client_id":"test","instrument":"BTC-USD","side":"SELL","type":"LIMIT","price":55000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	resp := decode(t, rec.Body)
	var data struct {
		Order struct {
			ID float64 `json:"id"`
		} `json:"order"`
	}
	json.Unmarshal(resp.Data, &data)
	return uint64(data.Order.ID)
}

func TestGetOrderByID(t *testing.T) {
	router := newTestRouter()
	id := submitTestOrder(t, router)

	req := httptest.NewRequest("GET", "/v1/orders/"+strconv.FormatUint(id, 10)+"?instrument=BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetOrderByIDNotFound(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/orders/999999?instrument=BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGetOrderByIDMissingInstrument(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/orders/1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGetOrderByIDInvalidID(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/orders/abc?instrument=BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestDeleteOrder(t *testing.T) {
	router := newTestRouter()
	id := submitTestOrder(t, router)

	req := httptest.NewRequest("DELETE", "/v1/orders/"+strconv.FormatUint(id, 10)+"?instrument=BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteOrderNotFound(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("DELETE", "/v1/orders/999999?instrument=BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestOrderByIDUnsupportedMethod(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("PUT", "/v1/orders/1?instrument=BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

// ── Order Book ───────────────────────────────────────────────────────────────

func TestGetOrderBook(t *testing.T) {
	router := newTestRouter()
	submitTestOrder(t, router)

	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD?depth=5", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGetOrderBookDefaultDepth(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGetOrderBookUnknown(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/orderbook/FAKE-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
}

// ── Stats ────────────────────────────────────────────────────────────────────

func TestGetGlobalStats(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/stats", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGetInstrumentStats(t *testing.T) {
	router := newTestRouter()
	// Submit an order so stats are populated
	submitTestOrder(t, router)

	req := httptest.NewRequest("GET", "/v1/stats/BTC-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetInstrumentStatsUnknown(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/stats/NOPE-USD", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
}

// ── WebSocket stub ───────────────────────────────────────────────────────────

func TestWebSocketDisabled(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/v1/ws", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (WS disabled)", rec.Code)
	}
}

// ── CORS ─────────────────────────────────────────────────────────────────────

func TestCORSPreflight(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("OPTIONS", "/v1/orders", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header")
	}
	if rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("missing Allow-Headers")
	}
}

func TestCORSHeadersOnResponse(t *testing.T) {
	router := newTestRouter()
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS origin header on regular response")
	}
}

// ── Submit and match (trades in response) ────────────────────────────────────

func TestSubmitOrderWithTrades(t *testing.T) {
	router := newTestRouter()

	// Place resting sell
	sell := `{"client_id":"s1","instrument":"BTC-USD","side":"SELL","type":"LIMIT","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(sell))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("sell status = %d", rec.Code)
	}

	// Place crossing buy
	buy := `{"client_id":"b1","instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":0.5}`
	req = httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(buy))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("buy status = %d", rec.Code)
	}

	resp := decode(t, rec.Body)
	var data struct {
		Order  map[string]interface{}   `json:"order"`
		Trades []map[string]interface{} `json:"trades"`
	}
	json.Unmarshal(resp.Data, &data)
	if len(data.Trades) != 1 {
		t.Errorf("expected 1 trade, got %d", len(data.Trades))
	}
	if data.Order["status"] != "FILLED" {
		t.Errorf("order status = %v", data.Order["status"])
	}
}

// ── Middleware with auth ─────────────────────────────────────────────────────

func TestRouterWithNilMiddleware(t *testing.T) {
	mc := metrics.NewCollector()
	me := engine.NewMatchingEngine([]string{"BTC-USD"}, mc)
	me.Start()
	defer me.Stop()

	router := api.NewRouter(me, mc, nil, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
}

// ── Market order types through API ───────────────────────────────────────────

func TestSubmitMarketOrderViaAPI(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"m1","instrument":"BTC-USD","side":"BUY","type":"MARKET","quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Should succeed (order rejected due to no liquidity, but API returns 201)
	if rec.Code != 201 {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSubmitFOKOrderViaAPI(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"f1","instrument":"BTC-USD","side":"BUY","type":"FOK","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitIOCOrderViaAPI(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"i1","instrument":"BTC-USD","side":"SELL","type":"IOC","price":50000,"quantity":1.0}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSubmitRejectsUnknownFields(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"u1","instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":1.0,"bogus_field":"nope"}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("expected 400 for unknown field, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSubmitRejectsTrailingGarbage(t *testing.T) {
	router := newTestRouter()
	body := `{"client_id":"t","instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":1.0}{"extra":"object"}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("expected 400 for multiple JSON objects, got %d", rec.Code)
	}
}
