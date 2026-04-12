package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func clearEnv() {
	for _, key := range []string{
		"ME_CONFIG_FILE", "ME_LISTEN_ADDR", "ME_INSTRUMENTS",
		"ME_TLS_ENABLED", "ME_TLS_CERT", "ME_TLS_KEY",
		"ME_WAL_ENABLED", "ME_WAL_DIR", "ME_WAL_SYNC_MODE", "ME_SNAPSHOT_EVERY",
		"ME_AUTH_ENABLED", "ME_API_KEYS_FILE",
		"ME_RATE_LIMIT_ENABLED", "ME_WRITE_LIMIT_PER_SEC", "ME_READ_LIMIT_PER_SEC",
		"ME_WRITE_BURST", "ME_READ_BURST",
	} {
		os.Unsetenv(key)
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %s", cfg.ListenAddr)
	}
	if len(cfg.Instruments) != 4 {
		t.Errorf("Instruments = %v", cfg.Instruments)
	}
	if cfg.WALSyncMode != "fdatasync" {
		t.Errorf("WALSyncMode = %s", cfg.WALSyncMode)
	}
	if cfg.SnapshotEvery != 100_000 {
		t.Errorf("SnapshotEvery = %d", cfg.SnapshotEvery)
	}
	if cfg.WriteLimitPerSec != 100 {
		t.Errorf("WriteLimitPerSec = %f", cfg.WriteLimitPerSec)
	}
	if cfg.ReadLimitPerSec != 1000 {
		t.Errorf("ReadLimitPerSec = %f", cfg.ReadLimitPerSec)
	}
}

func TestLoadDefaultsNoConfigFile(t *testing.T) {
	clearEnv()
	t.Setenv("ME_CONFIG_FILE", "/nonexistent/config.json")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %s, want :8080", cfg.ListenAddr)
	}
}

func TestLoadFromJSONFile(t *testing.T) {
	clearEnv()
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	data := `{"listen_addr": ":9090", "instruments": ["XRP-USD"]}`
	os.WriteFile(cfgFile, []byte(data), 0o644)

	t.Setenv("ME_CONFIG_FILE", cfgFile)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %s, want :9090", cfg.ListenAddr)
	}
	if len(cfg.Instruments) != 1 || cfg.Instruments[0] != "XRP-USD" {
		t.Errorf("Instruments = %v", cfg.Instruments)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	clearEnv()
	t.Setenv("ME_CONFIG_FILE", "/nonexistent")
	t.Setenv("ME_LISTEN_ADDR", ":3000")
	t.Setenv("ME_INSTRUMENTS", "BTC-USD,ETH-USD")
	t.Setenv("ME_WAL_ENABLED", "true")
	t.Setenv("ME_WAL_DIR", "/tmp/wal")
	t.Setenv("ME_WAL_SYNC_MODE", "fsync")
	t.Setenv("ME_SNAPSHOT_EVERY", "5000")
	t.Setenv("ME_RATE_LIMIT_ENABLED", "1")
	t.Setenv("ME_WRITE_LIMIT_PER_SEC", "50")
	t.Setenv("ME_READ_LIMIT_PER_SEC", "500")
	t.Setenv("ME_WRITE_BURST", "100")
	t.Setenv("ME_READ_BURST", "1000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != ":3000" {
		t.Errorf("ListenAddr = %s", cfg.ListenAddr)
	}
	if len(cfg.Instruments) != 2 {
		t.Errorf("Instruments = %v", cfg.Instruments)
	}
	if !cfg.WALEnabled {
		t.Error("WALEnabled should be true")
	}
	if cfg.WALDir != "/tmp/wal" {
		t.Errorf("WALDir = %s", cfg.WALDir)
	}
	if cfg.WALSyncMode != "fsync" {
		t.Errorf("WALSyncMode = %s", cfg.WALSyncMode)
	}
	if cfg.SnapshotEvery != 5000 {
		t.Errorf("SnapshotEvery = %d", cfg.SnapshotEvery)
	}
	if !cfg.RateLimitEnabled {
		t.Error("RateLimitEnabled should be true")
	}
	if cfg.WriteLimitPerSec != 50 {
		t.Errorf("WriteLimitPerSec = %f", cfg.WriteLimitPerSec)
	}
	if cfg.WriteBurst != 100 {
		t.Errorf("WriteBurst = %d", cfg.WriteBurst)
	}
}

func TestLoadTLSEnvOverrides(t *testing.T) {
	clearEnv()
	t.Setenv("ME_CONFIG_FILE", "/nonexistent")
	t.Setenv("ME_TLS_ENABLED", "yes")
	t.Setenv("ME_TLS_CERT", "/certs/cert.pem")
	t.Setenv("ME_TLS_KEY", "/certs/key.pem")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.TLSEnabled {
		t.Error("TLSEnabled should be true")
	}
	if cfg.TLSCertFile != "/certs/cert.pem" {
		t.Errorf("TLSCertFile = %s", cfg.TLSCertFile)
	}
	if cfg.TLSKeyFile != "/certs/key.pem" {
		t.Errorf("TLSKeyFile = %s", cfg.TLSKeyFile)
	}
}

func TestLoadAuthEnvOverrides(t *testing.T) {
	clearEnv()
	t.Setenv("ME_CONFIG_FILE", "/nonexistent")
	t.Setenv("ME_AUTH_ENABLED", "true")
	t.Setenv("ME_API_KEYS_FILE", "/keys.json")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.AuthEnabled {
		t.Error("AuthEnabled should be true")
	}
	if cfg.APIKeysFile != "/keys.json" {
		t.Errorf("APIKeysFile = %s", cfg.APIKeysFile)
	}
}

// ── Validate ─────────────────────────────────────────────────────────────────

func TestValidateEmptyListenAddr(t *testing.T) {
	cfg := Default()
	cfg.ListenAddr = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty listen_addr")
	}
}

func TestValidateNoInstruments(t *testing.T) {
	cfg := Default()
	cfg.Instruments = nil
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty instruments")
	}
}

func TestValidateTLSMissingCert(t *testing.T) {
	cfg := Default()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = ""
	cfg.TLSKeyFile = "key.pem"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing cert")
	}
}

func TestValidateTLSMissingKey(t *testing.T) {
	cfg := Default()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = "cert.pem"
	cfg.TLSKeyFile = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing key")
	}
}

func TestValidateWALBadSyncMode(t *testing.T) {
	cfg := Default()
	cfg.WALEnabled = true
	cfg.WALSyncMode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid sync mode")
	}
}

func TestValidateWALValidSyncModes(t *testing.T) {
	for _, mode := range []string{"fsync", "fdatasync", "none"} {
		cfg := Default()
		cfg.WALEnabled = true
		cfg.WALSyncMode = mode
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error for sync mode %s: %v", mode, err)
		}
	}
}

func TestValidateAuthMissingKeysFile(t *testing.T) {
	cfg := Default()
	cfg.AuthEnabled = true
	cfg.APIKeysFile = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing api_keys_file")
	}
}

func TestValidateDefaultPasses(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should pass validation: %v", err)
	}
}

// ── LoadAPIKeys ──────────────────────────────────────────────────────────────

func TestLoadAPIKeys(t *testing.T) {
	dir := t.TempDir()
	keysFile := filepath.Join(dir, "keys.json")
	keys := APIKeysConfig{
		Keys: map[string]APIKeyEntry{
			"test-key": {
				Secret:      "abc123",
				Permissions: []string{"trade", "read"},
				RateClass:   "default",
			},
		},
	}
	data, _ := json.Marshal(keys)
	os.WriteFile(keysFile, data, 0o644)

	loaded, err := LoadAPIKeys(keysFile)
	if err != nil {
		t.Fatalf("LoadAPIKeys: %v", err)
	}
	if len(loaded.Keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(loaded.Keys))
	}
	entry := loaded.Keys["test-key"]
	if entry.Secret != "abc123" {
		t.Errorf("Secret = %s", entry.Secret)
	}
	if len(entry.Permissions) != 2 {
		t.Errorf("Permissions = %v", entry.Permissions)
	}
}

func TestLoadAPIKeysFileNotFound(t *testing.T) {
	_, err := LoadAPIKeys("/nonexistent/keys.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadAPIKeysInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	keysFile := filepath.Join(dir, "bad.json")
	os.WriteFile(keysFile, []byte("{bad json"), 0o644)

	_, err := LoadAPIKeys(keysFile)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadInvalidJSONConfigFile(t *testing.T) {
	clearEnv()
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "bad.json")
	os.WriteFile(cfgFile, []byte("{bad"), 0o644)
	t.Setenv("ME_CONFIG_FILE", cfgFile)

	_, err := Load()
	if err == nil {
		t.Error("expected error for invalid config JSON")
	}
}

// ── parseBool ────────────────────────────────────────────────────────────────

func TestParseBool(t *testing.T) {
	cases := map[string]bool{
		"true": true, "TRUE": true, "True": true,
		"1": true, "yes": true, "YES": true,
		"false": false, "0": false, "no": false, "": false, "random": false,
	}
	for input, want := range cases {
		if got := parseBool(input); got != want {
			t.Errorf("parseBool(%q) = %v, want %v", input, got, want)
		}
	}
}
