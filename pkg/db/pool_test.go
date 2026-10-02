package db

import (
	"testing"
	"time"
)

func TestPoolSettingsFromEnvDefaults(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	t.Setenv("DB_CONN_MAX_LIFETIME", "")

	got := PoolSettingsFromEnv()

	if got.MaxOpenConns != 10 || got.MaxIdleConns != 5 || got.ConnMaxLifetime != 30*time.Minute {
		t.Fatalf("unexpected defaults %+v", got)
	}
}

func TestPoolSettingsFromEnvOverridesAndIgnoresBadValues(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "25")
	t.Setenv("DB_MAX_IDLE_CONNS", "-3")
	t.Setenv("DB_CONN_MAX_LIFETIME", "15m")

	got := PoolSettingsFromEnv()

	if got.MaxOpenConns != 25 || got.MaxIdleConns != 5 || got.ConnMaxLifetime != 15*time.Minute {
		t.Fatalf("unexpected settings %+v", got)
	}
}
