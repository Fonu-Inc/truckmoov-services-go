package db

import (
	"database/sql"
	"os"
	"strconv"
	"time"
)

const (
	defaultMaxOpenConns    = 10
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = 30 * time.Minute
)

// PoolSettings bounds a service's connections to Postgres, so a burst in one
// service cannot take every connection the server allows.
type PoolSettings struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// PoolSettingsFromEnv reads DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS and
// DB_CONN_MAX_LIFETIME (a Go duration such as 30m). A missing or unusable
// value keeps its default.
func PoolSettingsFromEnv() PoolSettings {
	return PoolSettings{
		MaxOpenConns:    positiveIntEnv("DB_MAX_OPEN_CONNS", defaultMaxOpenConns),
		MaxIdleConns:    positiveIntEnv("DB_MAX_IDLE_CONNS", defaultMaxIdleConns),
		ConnMaxLifetime: positiveDurationEnv("DB_CONN_MAX_LIFETIME", defaultConnMaxLifetime),
	}
}

func ConfigurePool(pool *sql.DB) {
	PoolSettingsFromEnv().Apply(pool)
}

func (s PoolSettings) Apply(pool *sql.DB) {
	pool.SetMaxOpenConns(s.MaxOpenConns)
	pool.SetMaxIdleConns(s.MaxIdleConns)
	pool.SetConnMaxLifetime(s.ConnMaxLifetime)
}

func positiveIntEnv(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveDurationEnv(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
