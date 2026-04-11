package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/config"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"READEST_EMAIL", "READEST_PASSWORD", "HARDCOVER_TOKEN",
		"SYNC_INTERVAL", "LISTEN_ADDR", "STATE_FILE",
		"ENABLE_TITLE_MATCH", "MANUAL_SYNC", "COVERS_DIR",
		"MIN_SYNC_PERCENT", "MIN_SYNC_PAGES",
	} {
		t.Setenv(k, "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "state.json", cfg.StateFile)
	assert.Equal(t, "covers", cfg.CoversDir)
	assert.False(t, cfg.EnableTitleMatch)
	assert.False(t, cfg.ManualSync)
	assert.Equal(t, float64(2), cfg.MinSyncPercent)
	assert.Equal(t, 5, cfg.MinSyncPages)
}

func TestLoad_AllEnvVars(t *testing.T) {
	clearEnv(t)
	t.Setenv("READEST_EMAIL", "a@b.com")
	t.Setenv("READEST_PASSWORD", "pass")
	t.Setenv("HARDCOVER_TOKEN", "tok")
	t.Setenv("SYNC_INTERVAL", "5m")
	t.Setenv("LISTEN_ADDR", ":9090")
	t.Setenv("STATE_FILE", "/tmp/s.json")
	t.Setenv("ENABLE_TITLE_MATCH", "true")
	t.Setenv("MANUAL_SYNC", "true")
	t.Setenv("COVERS_DIR", "/tmp/covers")
	t.Setenv("MIN_SYNC_PERCENT", "3.5")
	t.Setenv("MIN_SYNC_PAGES", "10")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "a@b.com", cfg.ReadestEmail)
	assert.Equal(t, "/tmp/covers", cfg.CoversDir)
	assert.Equal(t, "pass", cfg.ReadestPassword)
	assert.Equal(t, "tok", cfg.HardcoverToken)
	assert.Equal(t, ":9090", cfg.ListenAddr)
	assert.Equal(t, "/tmp/s.json", cfg.StateFile)
	assert.True(t, cfg.EnableTitleMatch)
	assert.True(t, cfg.ManualSync)
	assert.Equal(t, 3.5, cfg.MinSyncPercent)
	assert.Equal(t, 10, cfg.MinSyncPages)
}

func TestLoad_InvalidSyncInterval(t *testing.T) {
	clearEnv(t)
	t.Setenv("SYNC_INTERVAL", "not-a-duration")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SYNC_INTERVAL")
}

func TestLoad_InvalidMinSyncPercent(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIN_SYNC_PERCENT", "abc")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIN_SYNC_PERCENT")
}

func TestLoad_MinSyncPercentOutOfRange(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIN_SYNC_PERCENT", "-1")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIN_SYNC_PERCENT")

	t.Setenv("MIN_SYNC_PERCENT", "101")
	_, err = config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIN_SYNC_PERCENT")
}

func TestLoad_InvalidMinSyncPages(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIN_SYNC_PAGES", "abc")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIN_SYNC_PAGES")
}

func TestLoad_MinSyncPagesNegative(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIN_SYNC_PAGES", "-1")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIN_SYNC_PAGES")
}

func TestRequireReadest_Missing(t *testing.T) {
	cfg := &config.Config{}
	assert.Error(t, cfg.RequireReadest())

	cfg.ReadestEmail = "a@b.com"
	assert.Error(t, cfg.RequireReadest())
}

func TestRequireReadest_Valid(t *testing.T) {
	cfg := &config.Config{ReadestEmail: "a@b.com", ReadestPassword: "pass"}
	assert.NoError(t, cfg.RequireReadest())
}

func TestRequireHardcover_Missing(t *testing.T) {
	cfg := &config.Config{}
	assert.Error(t, cfg.RequireHardcover())
}

func TestRequireHardcover_Valid(t *testing.T) {
	cfg := &config.Config{HardcoverToken: "tok"}
	assert.NoError(t, cfg.RequireHardcover())
}
