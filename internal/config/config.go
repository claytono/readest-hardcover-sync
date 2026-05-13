package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ReadestEmail     string
	ReadestPassword  string
	HardcoverToken   string
	SyncInterval     time.Duration
	ListenAddr       string
	PublicBaseURL    string
	StateFile        string
	CoversDir        string
	SlackWebhookURL  string
	EnableTitleMatch bool
	ManualSync       bool
	MinSyncPercent   float64 // minimum progress % before syncing (default 2)
	MinSyncPages     int     // minimum Readest pages before syncing (default 5)
}

func Load() (*Config, error) {
	cfg := &Config{
		ReadestEmail:     os.Getenv("READEST_EMAIL"),
		ReadestPassword:  os.Getenv("READEST_PASSWORD"),
		HardcoverToken:   os.Getenv("HARDCOVER_TOKEN"),
		PublicBaseURL:    os.Getenv("PUBLIC_BASE_URL"),
		SlackWebhookURL:  os.Getenv("SLACK_WEBHOOK_URL"),
		SyncInterval:     10 * time.Minute,
		ListenAddr:       ":8080",
		StateFile:        "state.json",
		CoversDir:        "covers",
		EnableTitleMatch: os.Getenv("ENABLE_TITLE_MATCH") == "true",
		ManualSync:       os.Getenv("MANUAL_SYNC") == "true",
		MinSyncPercent:   2,
		MinSyncPages:     5,
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
	if v := os.Getenv("MIN_SYNC_PERCENT"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid MIN_SYNC_PERCENT: %w", err)
		}
		if f < 0 || f > 100 {
			return nil, fmt.Errorf("invalid MIN_SYNC_PERCENT: must be between 0 and 100")
		}
		cfg.MinSyncPercent = f
	}
	if v := os.Getenv("MIN_SYNC_PAGES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid MIN_SYNC_PAGES: %w", err)
		}
		if n < 0 {
			return nil, fmt.Errorf("invalid MIN_SYNC_PAGES: must be >= 0")
		}
		cfg.MinSyncPages = n
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
