package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("READEST_EMAIL", "")
	t.Setenv("READEST_PASSWORD", "")
	t.Setenv("HARDCOVER_TOKEN", "")
	t.Setenv("SYNC_INTERVAL", "")
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("STATE_FILE", "")
	t.Setenv("ENABLE_TITLE_MATCH", "")
	t.Setenv("MANUAL_SYNC", "")
	t.Setenv("COVERS_DIR", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "state.json", cfg.StateFile)
	assert.Equal(t, "covers", cfg.CoversDir)
	assert.False(t, cfg.EnableTitleMatch)
	assert.False(t, cfg.ManualSync)
}

func TestLoad_AllEnvVars(t *testing.T) {
	t.Setenv("READEST_EMAIL", "a@b.com")
	t.Setenv("READEST_PASSWORD", "pass")
	t.Setenv("HARDCOVER_TOKEN", "tok")
	t.Setenv("SYNC_INTERVAL", "5m")
	t.Setenv("LISTEN_ADDR", ":9090")
	t.Setenv("STATE_FILE", "/tmp/s.json")
	t.Setenv("ENABLE_TITLE_MATCH", "true")
	t.Setenv("MANUAL_SYNC", "true")
	t.Setenv("COVERS_DIR", "/tmp/covers")

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
}

func TestLoad_InvalidSyncInterval(t *testing.T) {
	t.Setenv("SYNC_INTERVAL", "not-a-duration")
	t.Setenv("READEST_EMAIL", "")
	t.Setenv("READEST_PASSWORD", "")
	t.Setenv("HARDCOVER_TOKEN", "")
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("STATE_FILE", "")
	t.Setenv("ENABLE_TITLE_MATCH", "")
	t.Setenv("MANUAL_SYNC", "")
	t.Setenv("COVERS_DIR", "")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SYNC_INTERVAL")
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
