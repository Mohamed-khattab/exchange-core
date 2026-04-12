package auth_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trading/matching-engine/internal/auth"
	"github.com/trading/matching-engine/internal/config"
)

var testSecret = hex.EncodeToString([]byte("test-secret-key-1234567890abcdef"))

func testKeys() *config.APIKeysConfig {
	return &config.APIKeysConfig{
		Keys: map[string]config.APIKeyEntry{
			"trader-1": {
				Secret:      testSecret,
				Permissions: []string{"trade", "read"},
			},
			"reader-1": {
				Secret:      testSecret,
				Permissions: []string{"read"},
			},
		},
	}
}

func signRequest(method, path, body, secret string) (timestamp, signature string) {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	signingString := strings.Join([]string{method, path, ts, body}, "\n")
	secretBytes, _ := hex.DecodeString(secret)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(signingString))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return ts, sig
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, _ := auth.ClientFromContext(r.Context())
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]string{"client": clientID})
	})
}

func TestPublicEndpointNoAuth(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200 for public endpoint, got %d", rec.Code)
	}
}

func TestMissingAPIKey(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestInvalidAPIKey(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	req.Header.Set("X-API-Key", "nonexistent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestValidReadRequest(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	req.Header.Set("X-API-Key", "reader-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReaderCannotTrade(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":1}`
	ts, sig := signRequest("POST", "/v1/orders", body, testSecret)

	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "reader-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestValidSignedWriteRequest(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD","side":"BUY","type":"LIMIT","price":50000,"quantity":1}`
	ts, sig := signRequest("POST", "/v1/orders", body, testSecret)

	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidSignature(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD"}`
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString([]byte("wrong")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestExpiredTimestamp(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD"}`
	oldTs := fmt.Sprintf("%d", time.Now().Add(-60*time.Second).UnixMilli())

	// Sign with old timestamp
	signingString := strings.Join([]string{"POST", "/v1/orders", oldTs, body}, "\n")
	secretBytes, _ := hex.DecodeString(testSecret)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(signingString))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	req.Header.Set("X-Timestamp", oldTs)
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMissingSignatureHeaders(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD"}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	// No timestamp or signature
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestInvalidTimestampFormat(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD"}`
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	req.Header.Set("X-Timestamp", "not-a-number")
	req.Header.Set("X-Signature", "dGVzdA==")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidSignatureEncoding(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD"}`
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", "!!!not-base64!!!")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteRequiresTradePermission(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	ts, sig := signRequest("DELETE", "/v1/orders/123", "", testSecret)
	req := httptest.NewRequest("DELETE", "/v1/orders/123?instrument=BTC-USD", nil)
	req.Header.Set("X-API-Key", "reader-1") // reader only
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestPublicInstrumentsEndpoint(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/v1/instruments", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200 for public /v1/instruments, got %d", rec.Code)
	}
}

func TestMissingSignatureOnly(t *testing.T) {
	mw := auth.Middleware(testKeys())
	handler := mw(okHandler())

	body := `{"instrument":"BTC-USD"}`
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	req := httptest.NewRequest("POST", "/v1/orders", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "trader-1")
	req.Header.Set("X-Timestamp", ts)
	// No X-Signature
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestContextPropagation(t *testing.T) {
	mw := auth.Middleware(testKeys())

	var gotClient string
	var gotPerms []string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClient, _ = auth.ClientFromContext(r.Context())
		gotPerms = auth.PermissionsFromContext(r.Context())
		w.WriteHeader(200)
	})
	handler := mw(inner)

	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	req.Header.Set("X-API-Key", "trader-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotClient != "trader-1" {
		t.Errorf("expected client trader-1, got %s", gotClient)
	}
	if len(gotPerms) != 2 {
		t.Errorf("expected 2 permissions, got %d", len(gotPerms))
	}
}
