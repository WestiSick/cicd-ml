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
	Bind                   string
	PostgresDSN            string
	RedisAddr              string
	MLBaseURL              string
	JWTSecret              string
	GithubWebhookSecret    string
	PublicAPIBase          string
	PublicWSBase           string
	BootstrapDefaultMonths int
	// ModelsDir is the path on the api-gateway container where the joblib
	// artifacts live — shared volume with ml-service so we can stream
	// downloads without proxying through the ML service. Defaults match the
	// compose mount.
	ModelsDir string
	// EnabledBGKinds is a comma-separated allow-list of bg_jobs kinds
	// the api-gateway's runner will claim. Empty = all kinds (single-
	// binary mode). When the collector + simulator are deployed as
	// separate containers, set this to limit the gateway to its own
	// kinds (bootstrap, compute_features, train_model) so it doesn't
	// race the dedicated workers for collect_history / refresh / simulate.
	EnabledBGKinds string
	// GatewayInternalBase is the URL the standalone collector / simulator
	// binaries use to push WS broadcasts back to the gateway. Defaults
	// to the in-docker hostname.
	GatewayInternalBase string
	LogLevel            zerolog.Level
}

func Load() Config {
	return Config{
		Bind:                   getenv("API_BIND", "0.0.0.0:8080"),
		PostgresDSN:            getenv("POSTGRES_DSN", ""),
		RedisAddr:              getenv("REDIS_ADDR", "redis:6379"),
		MLBaseURL:              getenv("ML_BASE_URL", "http://ml:8000"),
		JWTSecret:              getenv("JWT_SECRET", "dev"),
		GithubWebhookSecret:    getenv("GITHUB_WEBHOOK_SECRET", ""),
		PublicAPIBase:          getenv("PUBLIC_API_BASE", "http://localhost:8080"),
		PublicWSBase:           getenv("PUBLIC_WS_BASE", "ws://localhost:8080"),
		BootstrapDefaultMonths: getint("BOOTSTRAP_DEFAULT_MONTHS", 6),
		ModelsDir:              getenv("MODELS_DIR", "/var/lib/cicdml/models"),
		EnabledBGKinds:         getenv("ENABLED_BG_KINDS", ""),
		GatewayInternalBase:    getenv("GATEWAY_INTERNAL_BASE", "http://api:8080"),
		LogLevel:               parseLevel(getenv("LOG_LEVEL", "info")),
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
