// Package config centralises environment-driven configuration.
//
// All settings come from env vars (12-factor). The Load() function is
// intentionally simple — no Viper, no YAML overlays. The compose files are
// the single source of "deployment shape"; this package only reads what
// they pass in.
package config

import (
	"os"
	"strconv"

	"github.com/rs/zerolog"
)

type Config struct {
	Bind                 string
	PostgresDSN          string
	RedisAddr            string
	MLBaseURL            string
	JWTSecret            string
	GithubWebhookSecret  string
	PublicAPIBase        string
	PublicWSBase         string
	BootstrapDefaultMonths int
	LogLevel             zerolog.Level
}

func Load() Config {
	return Config{
		Bind:                  getenv("API_BIND", "0.0.0.0:8080"),
		PostgresDSN:           getenv("POSTGRES_DSN", ""),
		RedisAddr:             getenv("REDIS_ADDR", "redis:6379"),
		MLBaseURL:             getenv("ML_BASE_URL", "http://ml:8000"),
		JWTSecret:             getenv("JWT_SECRET", "dev"),
		GithubWebhookSecret:   getenv("GITHUB_WEBHOOK_SECRET", ""),
		PublicAPIBase:         getenv("PUBLIC_API_BASE", "http://localhost:8080"),
		PublicWSBase:          getenv("PUBLIC_WS_BASE", "ws://localhost:8080"),
		BootstrapDefaultMonths: getint("BOOTSTRAP_DEFAULT_MONTHS", 6),
		LogLevel:              parseLevel(getenv("LOG_LEVEL", "info")),
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func parseLevel(s string) zerolog.Level {
	lvl, err := zerolog.ParseLevel(s)
	if err != nil {
		return zerolog.InfoLevel
	}
	return lvl
}
