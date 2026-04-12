package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/trading/matching-engine/internal/api"
	"github.com/trading/matching-engine/internal/auth"
	"github.com/trading/matching-engine/internal/config"
	"github.com/trading/matching-engine/internal/engine"
	"github.com/trading/matching-engine/internal/metrics"
	"github.com/trading/matching-engine/internal/models"
	"github.com/trading/matching-engine/internal/ratelimit"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("[BOOT] Starting Trading Matching Engine v1.0.0")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[FATAL] Config error: %v", err)
	}

	// Initialize metrics collector
	mc := metrics.NewCollector()

	// Initialize matching engine with configured instruments
	engineCfg := engine.EngineConfig{
		WAL: engine.WALConfig{
			Enabled:       cfg.WALEnabled,
			Dir:           cfg.WALDir,
			SyncMode:      cfg.WALSyncMode,
			SnapshotEvery: cfg.SnapshotEvery,
		},
		STP: engine.STPConfig{
			Enabled:     cfg.STPEnabled,
			DefaultMode: models.STPMode(cfg.STPDefaultMode),
		},
	}
	if engineCfg.WAL.Enabled {
		log.Printf("[BOOT] WAL enabled (dir=%s, sync=%s, snapshot every %d events)",
			engineCfg.WAL.Dir, engineCfg.WAL.SyncMode, engineCfg.WAL.SnapshotEvery)
	}
	if engineCfg.STP.Enabled {
		log.Printf("[BOOT] STP enabled (default mode: %s)", engineCfg.STP.DefaultMode)
	}
	me := engine.NewMatchingEngine(cfg.Instruments, mc, engineCfg)
	me.Start()

	// Build optional middleware
	var authMW, rateLimitMW api.Middleware

	if cfg.AuthEnabled {
		keys, err := config.LoadAPIKeys(cfg.APIKeysFile)
		if err != nil {
			log.Fatalf("[FATAL] Failed to load API keys: %v", err)
		}
		log.Printf("[BOOT] Auth enabled with %d API keys", len(keys.Keys))
		authMW = auth.Middleware(keys)
	}

	// Shutdown context for background goroutines
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	if cfg.RateLimitEnabled {
		reg := ratelimit.NewRegistry(
			cfg.WriteLimitPerSec, cfg.WriteBurst,
			cfg.ReadLimitPerSec, cfg.ReadBurst,
		)
		reg.StartCleanup(shutdownCtx)
		rateLimitMW = reg.Middleware
		log.Printf("[BOOT] Rate limiting enabled (write: %.0f/s burst %d, read: %.0f/s burst %d)",
			cfg.WriteLimitPerSec, cfg.WriteBurst, cfg.ReadLimitPerSec, cfg.ReadBurst)
	}

	// Initialize REST API server
	router := api.NewRouter(me, mc, authMW, rateLimitMW)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if cfg.TLSEnabled {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if cfg.TLSEnabled {
			log.Printf("[BOOT] HTTPS server listening on %s", srv.Addr)
			if err := srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[FATAL] TLS server error: %v", err)
			}
		} else {
			log.Printf("[BOOT] HTTP server listening on %s (TLS disabled)", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[FATAL] Server error: %v", err)
			}
		}
	}()

	<-quit
	log.Println("[SHUTDOWN] Signal received, shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("[FATAL] Forced shutdown: %v", err)
	}

	shutdownCancel() // stop background goroutines (rate limit cleanup, etc.)
	me.Stop()
	log.Println("[SHUTDOWN] Engine stopped. Goodbye.")
}
