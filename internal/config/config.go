package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	ReadestEmail     string
	ReadestPassword  string
	HardcoverToken   string
	SyncInterval     time.Duration
	ListenAddr       string
	StateFile        string
	CoversDir        string
	EnableTitleMatch bool
	ManualSync       bool
}

func Load() (*Config, error) {
	cfg := &Config{
		ReadestEmail:     os.Getenv("READEST_EMAIL"),
		ReadestPassword:  os.Getenv("READEST_PASSWORD"),
		HardcoverToken:   os.Getenv("HARDCOVER_TOKEN"),
		SyncInterval:     10 * time.Minute,
		ListenAddr:       ":8080",
		StateFile:        "state.json",
		CoversDir:        "covers",
		EnableTitleMatch: os.Getenv("ENABLE_TITLE_MATCH") == "true",
		ManualSync:       os.Getenv("MANUAL_SYNC") == "true",
	}

	if v := os.Getenv("SYNC_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SYNC_INTERVAL: %w", err)
		}
		cfg.SyncInterval = d
	}
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("STATE_FILE"); v != "" {
		cfg.StateFile = v
	}
	if v := os.Getenv("COVERS_DIR"); v != "" {
		cfg.CoversDir = v
	}

	return cfg, nil
}

// RequireReadest returns an error if Readest credentials are not configured.
func (c *Config) RequireReadest() error {
	if c.ReadestEmail == "" || c.ReadestPassword == "" {
		return fmt.Errorf("READEST_EMAIL and READEST_PASSWORD are required")
	}
	return nil
}

// RequireHardcover returns an error if the Hardcover token is not configured.
func (c *Config) RequireHardcover() error {
	if c.HardcoverToken == "" {
		return fmt.Errorf("HARDCOVER_TOKEN is required")
	}
	return nil
}
