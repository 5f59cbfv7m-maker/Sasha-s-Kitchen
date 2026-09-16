// Package config loads all runtime configuration from the environment.
//
// Rules: every setting has a safe default for local development, secrets never
// have a default, and Load fails loudly at boot rather than at first request.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env             string // dev | staging | prod
	HTTPAddr        string // ":8080"
	PublicBaseURL   string // canonical origin, used to build absolute URLs
	ShutdownTimeout time.Duration

	Postgres PostgresConfig
	Redis    RedisConfig
	Storage  StorageConfig
	Auth     AuthConfig

	// RateLimitRPS is the sustained per-client request rate for public endpoints.
	RateLimitRPS   int
	RateLimitBurst int

	// FeedCacheTTL bounds how stale an anonymous storefront response may be.
	FeedCacheTTL time.Duration
}

type PostgresConfig struct {
	DSN string
	// MaxConns should be sized against the database, not the app: total
	// connections across all API replicas must stay under Postgres max_connections.
	MaxConns         int32
	MinConns         int32
	MaxConnLifetime  time.Duration
	MaxConnIdleTime  time.Duration
	StatementTimeout time.Duration
	ConnectTimeout   time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	// Disabled lets the API run without Redis; caching degrades to pass-through
	// rather than failing requests.
	Disabled bool
}

type StorageConfig struct {
	Endpoint  string // S3-compatible endpoint
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PublicCDN string // CDN origin that fronts the bucket
	UseSSL    bool
}

type AuthConfig struct {
	JWTSecret       []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AppleTeamID     string
	AppleClientID   string
	AppleKeyID      string
}

func (c Config) IsProd() bool { return c.Env == "prod" }

// Load reads configuration from the environment. In prod it refuses to start
// without the secrets that must never fall back to a development default.
func Load() (Config, error) {
	cfg := Config{
		Env:             env("APP_ENV", "dev"),
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		PublicBaseURL:   env("PUBLIC_BASE_URL", "http://localhost:8080"),
		ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 20*time.Second),
		RateLimitRPS:    envInt("RATE_LIMIT_RPS", 20),
		RateLimitBurst:  envInt("RATE_LIMIT_BURST", 60),
		FeedCacheTTL:    envDuration("FEED_CACHE_TTL", 60*time.Second),
		Postgres: PostgresConfig{
			DSN:              env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/sk_store?sslmode=disable"),
			MaxConns:         int32(envInt("DB_MAX_CONNS", 20)),
			MinConns:         int32(envInt("DB_MIN_CONNS", 2)),
			MaxConnLifetime:  envDuration("DB_CONN_MAX_LIFETIME", time.Hour),
			MaxConnIdleTime:  envDuration("DB_CONN_MAX_IDLE", 30*time.Minute),
			StatementTimeout: envDuration("DB_STATEMENT_TIMEOUT", 5*time.Second),
			ConnectTimeout:   envDuration("DB_CONNECT_TIMEOUT", 5*time.Second),
		},
		Redis: RedisConfig{
			Addr:     env("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       envInt("REDIS_DB", 0),
			Disabled: envBool("REDIS_DISABLED", false),
		},
		Storage: StorageConfig{
			Endpoint:  env("S3_ENDPOINT", ""),
			Region:    env("S3_REGION", "us-east-1"),
			Bucket:    env("S3_BUCKET", "sk-media"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"),
			SecretKey: os.Getenv("S3_SECRET_KEY"),
			PublicCDN: env("MEDIA_CDN_BASE", ""),
			UseSSL:    envBool("S3_USE_SSL", true),
		},
		Auth: AuthConfig{
			JWTSecret:       []byte(os.Getenv("JWT_SECRET")),
			AccessTokenTTL:  envDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL: envDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
			AppleTeamID:     os.Getenv("APPLE_TEAM_ID"),
			AppleClientID:   os.Getenv("APPLE_CLIENT_ID"),
			AppleKeyID:      os.Getenv("APPLE_KEY_ID"),
		},
	}

	if len(cfg.Auth.JWTSecret) == 0 {
		if cfg.IsProd() {
			return Config{}, errors.New("config: JWT_SECRET is required in prod")
		}
		// Deterministic dev-only secret. Never reached in prod because of the check above.
		cfg.Auth.JWTSecret = []byte("dev-insecure-secret-do-not-use-in-production")
	}
	if cfg.IsProd() && len(cfg.Auth.JWTSecret) < 32 {
		return Config{}, errors.New("config: JWT_SECRET must be at least 32 bytes in prod")
	}
	if cfg.Postgres.MaxConns < cfg.Postgres.MinConns {
		return Config{}, fmt.Errorf("config: DB_MAX_CONNS (%d) < DB_MIN_CONNS (%d)",
			cfg.Postgres.MaxConns, cfg.Postgres.MinConns)
	}
	return cfg, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}
