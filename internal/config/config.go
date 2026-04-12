package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr  string   `json:"listen_addr"`
	Instruments []string `json:"instruments"`

	// TLS
	TLSEnabled  bool   `json:"tls_enabled"`
	TLSCertFile string `json:"tls_cert_file"`
	TLSKeyFile  string `json:"tls_key_file"`

	// WAL
	WALEnabled    bool   `json:"wal_enabled"`
	WALDir        string `json:"wal_dir"`
	WALSyncMode   string `json:"wal_sync_mode"` // "fdatasync" | "fsync" | "none"
	SnapshotEvery int    `json:"snapshot_every"` // events between snapshots

	// Auth
	AuthEnabled bool   `json:"auth_enabled"`
	APIKeysFile string `json:"api_keys_file"`

	// Rate Limiting
	RateLimitEnabled bool    `json:"rate_limit_enabled"`
	WriteLimitPerSec float64 `json:"write_limit_per_sec"`
	ReadLimitPerSec  float64 `json:"read_limit_per_sec"`
	WriteBurst       int     `json:"write_burst"`
	ReadBurst        int     `json:"read_burst"`
}

type APIKeyEntry struct {
	Secret      string   `json:"secret"`
	Permissions []string `json:"permissions"`
	RateClass   string   `json:"rate_class"`
}

type APIKeysConfig struct {
	Keys map[string]APIKeyEntry `json:"keys"`
}

func Default() *Config {
	return &Config{
		ListenAddr:       ":8080",
		Instruments:      []string{"BTC-USD", "ETH-USD", "SOL-USD", "BNB-USD"},
		WALDir:           "./data/wal",
		WALSyncMode:      "fdatasync",
		SnapshotEvery:    100_000,
		WriteLimitPerSec: 100,
		ReadLimitPerSec:  1000,
		WriteBurst:       200,
		ReadBurst:        2000,
	}
}

// Load reads config from a JSON file (if it exists) then applies env var overrides.
func Load() (*Config, error) {
	cfg := Default()

	// Load JSON config file if specified or if default exists
	configFile := envOrDefault("ME_CONFIG_FILE", "./config.json")
	if data, err := os.ReadFile(configFile); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", configFile, err)
		}
	}

	// Env var overrides
	if v := os.Getenv("ME_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("ME_INSTRUMENTS"); v != "" {
		cfg.Instruments = strings.Split(v, ",")
	}

	// TLS
	if v := os.Getenv("ME_TLS_ENABLED"); v != "" {
		cfg.TLSEnabled = parseBool(v)
	}
	if v := os.Getenv("ME_TLS_CERT"); v != "" {
		cfg.TLSCertFile = v
	}
	if v := os.Getenv("ME_TLS_KEY"); v != "" {
		cfg.TLSKeyFile = v
	}

	// WAL
	if v := os.Getenv("ME_WAL_ENABLED"); v != "" {
		cfg.WALEnabled = parseBool(v)
	}
	if v := os.Getenv("ME_WAL_DIR"); v != "" {
		cfg.WALDir = v
	}
	if v := os.Getenv("ME_WAL_SYNC_MODE"); v != "" {
		cfg.WALSyncMode = v
	}
	if v := os.Getenv("ME_SNAPSHOT_EVERY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.SnapshotEvery = n
		}
	}

	// Auth
	if v := os.Getenv("ME_AUTH_ENABLED"); v != "" {
		cfg.AuthEnabled = parseBool(v)
	}
	if v := os.Getenv("ME_API_KEYS_FILE"); v != "" {
		cfg.APIKeysFile = v
	}

	// Rate Limiting
	if v := os.Getenv("ME_RATE_LIMIT_ENABLED"); v != "" {
		cfg.RateLimitEnabled = parseBool(v)
	}
	if v := os.Getenv("ME_WRITE_LIMIT_PER_SEC"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.WriteLimitPerSec = f
		}
	}
	if v := os.Getenv("ME_READ_LIMIT_PER_SEC"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ReadLimitPerSec = f
		}
	}
	if v := os.Getenv("ME_WRITE_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WriteBurst = n
		}
	}
	if v := os.Getenv("ME_READ_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ReadBurst = n
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadAPIKeys reads the API keys file.
func LoadAPIKeys(path string) (*APIKeysConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading API keys file: %w", err)
	}
	var keys APIKeysConfig
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("parsing API keys file: %w", err)
	}
	return &keys, nil
}

func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if len(c.Instruments) == 0 {
		return fmt.Errorf("at least one instrument is required")
	}
	if c.TLSEnabled {
		if c.TLSCertFile == "" || c.TLSKeyFile == "" {
			return fmt.Errorf("tls_cert_file and tls_key_file required when TLS is enabled")
		}
	}
	if c.WALEnabled {
		switch c.WALSyncMode {
		case "fsync", "fdatasync", "none":
		default:
			return fmt.Errorf("wal_sync_mode must be fsync, fdatasync, or none")
		}
	}
	if c.AuthEnabled && c.APIKeysFile == "" {
		return fmt.Errorf("api_keys_file required when auth is enabled")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "true" || s == "1" || s == "yes"
}
