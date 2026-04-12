package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/trading/matching-engine/internal/config"
)

type contextKey string

const (
	ctxAPIKeyID    contextKey = "api_key_id"
	ctxPermissions contextKey = "permissions"
)

// ClientFromContext returns the API key ID of the authenticated client.
func ClientFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxAPIKeyID).(string)
	return v, ok
}

// PermissionsFromContext returns the permissions of the authenticated client.
func PermissionsFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(ctxPermissions).([]string)
	return v
}

// HasPermission checks if the context carries the given permission.
func HasPermission(ctx context.Context, perm string) bool {
	for _, p := range PermissionsFromContext(ctx) {
		if p == perm {
			return true
		}
	}
	return false
}

// publicPaths are endpoints that require no authentication.
var publicPaths = map[string]bool{
	"/health":          true,
	"/v1/instruments":  true,
}

func isPublicPath(path string) bool {
	return publicPaths[path]
}

// isWriteRequest returns true for requests that mutate state.
func isWriteRequest(r *http.Request) bool {
	return r.Method == http.MethodPost || r.Method == http.MethodDelete
}

type apiResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func writeAuthError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(apiResponse{Status: "error", Error: msg})
}

// Middleware returns an HTTP middleware that enforces API key authentication
// and HMAC signature verification for write endpoints.
func Middleware(keys *config.APIKeysConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Public endpoints pass through
			if isPublicPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract API key
			apiKeyID := r.Header.Get("X-API-Key")
			if apiKeyID == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing X-API-Key header")
				return
			}

			entry, ok := keys.Keys[apiKeyID]
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "invalid API key")
				return
			}

			// For write requests, verify HMAC signature
			if isWriteRequest(r) {
				if err := verifySignature(r, entry.Secret); err != nil {
					writeAuthError(w, http.StatusUnauthorized, err.Error())
					return
				}
			}

			// Set identity in context
			ctx := context.WithValue(r.Context(), ctxAPIKeyID, apiKeyID)
			ctx = context.WithValue(ctx, ctxPermissions, entry.Permissions)

			// Check permissions
			if isWriteRequest(r) {
				if !HasPermission(ctx, "trade") {
					writeAuthError(w, http.StatusForbidden, "insufficient permissions: trade required")
					return
				}
			} else {
				if !HasPermission(ctx, "read") {
					writeAuthError(w, http.StatusForbidden, "insufficient permissions: read required")
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

const maxTimestampAge = 30 * time.Second

func verifySignature(r *http.Request, secretHex string) error {
	tsStr := r.Header.Get("X-Timestamp")
	if tsStr == "" {
		return fmt.Errorf("missing X-Timestamp header")
	}

	sigStr := r.Header.Get("X-Signature")
	if sigStr == "" {
		return fmt.Errorf("missing X-Signature header")
	}

	// Parse and validate timestamp
	tsMillis, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid X-Timestamp")
	}

	requestTime := time.UnixMilli(tsMillis)
	age := time.Since(requestTime)
	if age < 0 {
		age = -age
	}
	// Also guard against absurdly large values
	if age > maxTimestampAge || age > time.Duration(math.MaxInt64/2) {
		return fmt.Errorf("request expired")
	}

	// Read body for signing
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body")
	}
	// Replace body so downstream handlers can read it
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Construct signing string: METHOD\nPATH\nTIMESTAMP\nBODY
	signingString := strings.Join([]string{
		r.Method,
		r.URL.Path,
		tsStr,
		string(bodyBytes),
	}, "\n")

	// Decode the hex secret
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return fmt.Errorf("server configuration error: invalid secret encoding")
	}

	// Compute expected HMAC-SHA256
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingString))
	expected := mac.Sum(nil)

	// Decode the provided signature (base64)
	provided, err := base64.StdEncoding.DecodeString(sigStr)
	if err != nil {
		return fmt.Errorf("invalid X-Signature encoding")
	}

	// Constant-time comparison
	if !hmac.Equal(expected, provided) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}
