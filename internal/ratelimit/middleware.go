package ratelimit

import (
	"encoding/json"
	"net/http"

	"github.com/trading/matching-engine/internal/auth"
)

type apiResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Middleware returns an HTTP middleware that enforces per-client rate limits.
// It uses the client identity set by the auth middleware.
// Requests without auth context (public endpoints) bypass rate limiting.
func (reg *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, ok := auth.ClientFromContext(r.Context())
		if !ok {
			// No auth context = public endpoint or auth disabled, skip rate limiting
			next.ServeHTTP(w, r)
			return
		}

		isWrite := r.Method == http.MethodPost || r.Method == http.MethodDelete
		if !reg.Allow(clientID, isWrite) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(apiResponse{
				Status: "error",
				Error:  "rate limit exceeded",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}
