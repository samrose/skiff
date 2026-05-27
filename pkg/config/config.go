package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Config holds all runtime configuration for skiff binaries, loaded from
// environment variables. Call Load() once at startup.
type Config struct {
	// Temporal
	TemporalHost      string
	TemporalNamespace string
	TemporalTaskQueue string

	// ClickHouse
	ClickHouseDSN string

	// S3 / Garage
	S3Endpoint        string
	S3Region          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3BucketSources   string
	S3BucketCache     string

	// Cache
	CachePublicURL      string
	CacheSigningKey     string // raw "name:base64-secret"
	CacheSigningKeyFile string // path to file; wins over CacheSigningKey when both set

	// Registry
	RegistryURL          string
	RegistryUserAgent    string
	RegistryPollInterval time.Duration
	PackumentURL         string // URL base for packument/tarball fetches (may differ from RegistryURL)

	// Build targets
	NodejsVersionTag string
	TargetSystem     string

	// HTTP
	ListenAddr  string
	MetricsAddr string

	// Logging
	LogLevel string
}

// Load reads environment variables into a Config, applies defaults, and validates
// required fields. It returns an error listing all missing required variables.
func Load() (*Config, error) {
	c := &Config{
		TemporalHost:        getenv("TEMPORAL_HOST", "temporal:7233"),
		TemporalNamespace:   getenv("TEMPORAL_NAMESPACE", "default"),
		TemporalTaskQueue:   getenv("TEMPORAL_TASK_QUEUE", "skiff-default"),
		ClickHouseDSN:       getenv("CLICKHOUSE_DSN", "clickhouse://default:@clickhouse:9000/skiff"),
		S3Endpoint:          getenv("S3_ENDPOINT", "http://garage:3900"),
		S3Region:            getenv("S3_REGION", "garage"),
		S3AccessKeyID:       getenv("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey:   getenv("S3_SECRET_ACCESS_KEY", ""),
		S3BucketSources:     getenv("S3_BUCKET_SOURCES", "skiff-sources"),
		S3BucketCache:       getenv("S3_BUCKET_CACHE", "skiff-cache"),
		CachePublicURL:      getenv("CACHE_PUBLIC_URL", "http://localhost:8081/cache"),
		CacheSigningKey:     getenv("CACHE_SIGNING_KEY", ""),
		CacheSigningKeyFile: getenv("CACHE_SIGNING_KEY_FILE", "/run/secrets/cache-signing-key"),
		RegistryURL:         getenv("REGISTRY_URL", "https://replicate.npmjs.com"),
		RegistryUserAgent:   getenv("REGISTRY_USER_AGENT", "skiff-ingest/0.1 (+contact@example.invalid)"),
		PackumentURL:        getenv("PACKUMENT_URL", "https://registry.npmjs.org"),
		NodejsVersionTag:    getenv("NODEJS_VERSION_TAG", "20"),
		TargetSystem:        getenv("TARGET_SYSTEM", "x86_64-linux"),
		ListenAddr:          getenv("LISTEN_ADDR", ":8081"),
		MetricsAddr:         getenv("METRICS_ADDR", ":9090"),
		LogLevel:            getenv("LOG_LEVEL", "info"),
	}

	pollStr := getenv("REGISTRY_POLL_INTERVAL", "2s")
	dur, err := time.ParseDuration(pollStr)
	if err != nil {
		return nil, fmt.Errorf("config: REGISTRY_POLL_INTERVAL %q: %w", pollStr, err)
	}
	c.RegistryPollInterval = dur

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// validate checks that all required fields are set.
func (c *Config) validate() error {
	var errs []error
	if c.ClickHouseDSN == "" {
		errs = append(errs, errors.New("CLICKHOUSE_DSN is required"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed: %w", errors.Join(errs...))
}

// LogValue returns a slog.Value with secrets redacted, suitable for startup logs.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("temporal_host", c.TemporalHost),
		slog.String("temporal_namespace", c.TemporalNamespace),
		slog.String("temporal_task_queue", c.TemporalTaskQueue),
		slog.String("clickhouse_dsn", redactDSN(c.ClickHouseDSN)),
		slog.String("s3_endpoint", c.S3Endpoint),
		slog.String("s3_region", c.S3Region),
		slog.String("s3_access_key_id", redactSecret(c.S3AccessKeyID)),
		slog.String("s3_secret_access_key", redact),
		slog.String("s3_bucket_sources", c.S3BucketSources),
		slog.String("s3_bucket_cache", c.S3BucketCache),
		slog.String("cache_public_url", c.CachePublicURL),
		slog.String("cache_signing_key", redact), //nolint:gocritic
		slog.String("cache_signing_key_file", c.CacheSigningKeyFile),
		slog.String("registry_url", c.RegistryURL),
		slog.String("packument_url", c.PackumentURL),
		slog.String("registry_user_agent", c.RegistryUserAgent),
		slog.Duration("registry_poll_interval", c.RegistryPollInterval),
		slog.String("nodejs_version_tag", c.NodejsVersionTag),
		slog.String("target_system", c.TargetSystem),
		slog.String("listen_addr", c.ListenAddr),
		slog.String("metrics_addr", c.MetricsAddr),
		slog.String("log_level", c.LogLevel),
	)
}

const redact = "[redacted]"

func getenv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// redactDSN removes the password component from a DSN for log output.
func redactDSN(dsn string) string {
	// Simple approach: if the DSN contains a password segment, mask it.
	// clickhouse://user:password@host:port/db → clickhouse://user:[redacted]@host:port/db
	// We use a basic scan rather than importing net/url to keep the dep light.
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == ':' {
			// Find the @ after this colon — that span is the password.
			rest := dsn[i+1:]
			for j := 0; j < len(rest); j++ {
				if rest[j] == '@' && j > 0 {
					return dsn[:i+1] + redact + "@" + rest[j+1:]
				}
			}
		}
	}
	return dsn
}

// redactSecret shows only the first 4 chars of a non-empty secret, otherwise redacts.
func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return redact
	}
	return s[:4] + "..."
}
