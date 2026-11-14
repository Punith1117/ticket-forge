package database

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// Config holds PostgreSQL connection pool configuration.
type Config struct {
	Host            string
	Port            string
	User            string
	Password        string
	Database        string
	SSLMode         string
	MaxConns        int32
	MinConns        int32
	MaxConnIdleTime time.Duration
	MaxConnLifetime time.Duration
}

// ConfigFromEnv reads database configuration from environment variables,
// falling back to defaults appropriate for local development.
func ConfigFromEnv() Config {
	// Attempt to load .env file if present, ignoring error if it does not exist
	_ = godotenv.Load()

	cfg := Config{
		Host:            getEnv("POSTGRES_HOST", "localhost"),
		Port:            getEnv("POSTGRES_PORT", "5433"),
		User:            getEnv("POSTGRES_USER", "ticketforge"),
		Password:        getEnv("POSTGRES_PASSWORD", "ticketforge_dev_password"),
		Database:        getEnv("POSTGRES_DB", "ticketforge"),
		SSLMode:         getEnv("POSTGRES_SSLMODE", "disable"),
		MaxConns:        25,
		MinConns:        2,
		MaxConnIdleTime: 15 * time.Minute,
		MaxConnLifetime: 1 * time.Hour,
	}

	if v := os.Getenv("POSTGRES_MAX_CONNS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			cfg.MaxConns = int32(n)
		}
	}

	if v := os.Getenv("POSTGRES_MIN_CONNS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			cfg.MinConns = int32(n)
		}
	}

	return cfg
}

// ConnectionString formats the database connection URL.
func (c Config) ConnectionString() string {
	userInfo := url.UserPassword(c.User, c.Password)
	hostPort := fmt.Sprintf("%s:%s", c.Host, c.Port)
	query := url.Values{}
	query.Set("sslmode", c.SSLMode)

	u := url.URL{
		Scheme:   "postgres",
		User:     userInfo,
		Host:     hostPort,
		Path:     c.Database,
		RawQuery: query.Encode(),
	}

	return u.String()
}

// NewPool initializes and validates a new PostgreSQL connection pool.
func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("parsing db connection string: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating db connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
