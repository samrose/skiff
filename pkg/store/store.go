package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Store holds the ClickHouse connection and exposes typed accessors.
type Store struct {
	conn driver.Conn
}

// Open parses dsn, dials ClickHouse, pings the server, and returns a ready Store.
func Open(ctx context.Context, dsn string) (*Store, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse dsn: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{conn: conn}, nil
}

// Close releases the underlying connection.
func (s *Store) Close() error {
	return s.conn.Close()
}

// ---------------------------------------------------------------------------
// Value types
// ---------------------------------------------------------------------------

// Observation represents a row in package_observations.
type Observation struct {
	ObservedAt       time.Time
	Name             string
	Version          string
	RegistrySeq      uint64
	PackumentRev     string
	TarballURL       string
	TarballSHA512Hex string // 128 hex chars
	TarballSizeBytes uint64
	SourceObjectKey  string
}

// Classification represents a row in classifications.
type Classification struct {
	ClassifiedAt      time.Time
	Name              string
	Version           string
	Classification    string // enum value: broken, suspicious, has_native_code, etc.
	Reason            string
	RuleMatched       string
	ClassifierVersion string
}

// BuildKey identifies the (name, version, system, nodejs_version) tuple used when
// recording a build start.
type BuildKey struct {
	Name          string
	Version       string
	System        string
	NodejsVersion string
}

// BuildSuccess carries the full result of a successful build.
type BuildSuccess struct {
	BuildKey
	StorePath       string
	NarSHA256Hex    string // 64 hex chars
	NarSizeBytes    uint64
	FileSHA256Hex   string // 64 hex chars
	FileSizeBytes   uint64
	BuildDurationMS uint32
}

// BuildFailure carries the result of a failed build.
type BuildFailure struct {
	BuildKey
	LogExcerpt      string
	BuildDurationMS uint32
}

// Build represents a row read back from skiff.builds.
type Build struct {
	BuiltAt         time.Time
	Name            string
	Version         string
	System          string
	NodejsVersion   string
	Status          string // "success" | "failed"
	StorePath       string
	NarSHA256Hex    string
	NarSizeBytes    uint64
	FileSHA256Hex   string
	FileSizeBytes   uint64
	BuildDurationMS uint32
	LogExcerpt      string
}

// CachePublication represents a row in cache_publications.
type CachePublication struct {
	Name             string
	Version          string
	System           string
	NodejsVersion    string
	StorePath        string
	NarinfoObjectKey string
	NarObjectKey     string
	CacheURLBase     string
}

// Event represents a row in the events append-only log.
type Event struct {
	EventType  string // enum value: package_observed, classified, etc.
	Name       string
	Version    string
	WorkflowID string
	Payload    string // JSON
}

// ---------------------------------------------------------------------------
// Ingest checkpoint
// ---------------------------------------------------------------------------

// GetChangesCheckpoint reads the most recently checkpointed sequence number.
// Returns 0 if no checkpoint has been stored yet.
func (s *Store) GetChangesCheckpoint(ctx context.Context) (uint64, error) {
	var val string
	err := s.conn.QueryRow(
		ctx,
		`SELECT value FROM skiff.ingest_state FINAL WHERE key = 'changes_feed_seq'`,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: get changes checkpoint: %w", err)
	}
	var seq uint64
	if _, err := fmt.Sscanf(val, "%d", &seq); err != nil {
		return 0, fmt.Errorf("store: parse checkpoint value %q: %w", val, err)
	}
	return seq, nil
}

// SetChangesCheckpoint persists the latest sequence number.
func (s *Store) SetChangesCheckpoint(ctx context.Context, seq uint64) error {
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.ingest_state (key, value) VALUES ('changes_feed_seq', ?)`,
		fmt.Sprintf("%d", seq),
	); err != nil {
		return fmt.Errorf("store: set checkpoint: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Observations
// ---------------------------------------------------------------------------

// RecordObservation appends a row to package_observations.
func (s *Store) RecordObservation(ctx context.Context, obs Observation) error {
	observedAt := obs.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.package_observations
		 (observed_at, name, version, registry_seq, packument_rev,
		  tarball_url, tarball_sha512_hex, tarball_size_bytes, source_object_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		observedAt, obs.Name, obs.Version, obs.RegistrySeq,
		obs.PackumentRev, obs.TarballURL,
		obs.TarballSHA512Hex, obs.TarballSizeBytes, obs.SourceObjectKey,
	); err != nil {
		return fmt.Errorf("store: record observation: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Classifications
// ---------------------------------------------------------------------------

// RecordClassification inserts or replaces the classification for (name, version).
func (s *Store) RecordClassification(ctx context.Context, c Classification) error {
	classifiedAt := c.ClassifiedAt
	if classifiedAt.IsZero() {
		classifiedAt = time.Now().UTC()
	}
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.classifications
		 (classified_at, name, version, classification, reason, rule_matched, classifier_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		classifiedAt, c.Name, c.Version, c.Classification,
		c.Reason, c.RuleMatched, c.ClassifierVersion,
	); err != nil {
		return fmt.Errorf("store: record classification: %w", err)
	}
	return nil
}

// GetLatestClassification returns the canonical classification for (name, version).
// Returns (zero, false, nil) when no row exists.
func (s *Store) GetLatestClassification(ctx context.Context, name, version string) (Classification, bool, error) {
	var c Classification
	err := s.conn.QueryRow(
		ctx,
		`SELECT classified_at, name, version, classification, reason, rule_matched, classifier_version
		 FROM skiff.classifications FINAL
		 WHERE name = ? AND version = ?`,
		name, version,
	).Scan(
		&c.ClassifiedAt, &c.Name, &c.Version, &c.Classification,
		&c.Reason, &c.RuleMatched, &c.ClassifierVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Classification{}, false, nil
	}
	if err != nil {
		return Classification{}, false, fmt.Errorf("store: get classification: %w", err)
	}
	return c, true, nil
}

// ---------------------------------------------------------------------------
// Builds
// ---------------------------------------------------------------------------

// RecordBuildStarted inserts a "failed" placeholder build row so the key exists
// while the build is in flight. The activity updates it on success or failure.
func (s *Store) RecordBuildStarted(ctx context.Context, b BuildKey) error {
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.builds
		 (name, version, system, nodejs_version, status)
		 VALUES (?, ?, ?, ?, 'failed')`,
		b.Name, b.Version, b.System, b.NodejsVersion,
	); err != nil {
		return fmt.Errorf("store: record build started: %w", err)
	}
	return nil
}

// RecordBuildSuccess replaces the placeholder row with a successful build result.
func (s *Store) RecordBuildSuccess(ctx context.Context, b BuildSuccess) error {
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.builds
		 (name, version, system, nodejs_version, status,
		  store_path, nar_sha256_hex, nar_size_bytes,
		  file_sha256_hex, file_size_bytes, build_duration_ms)
		 VALUES (?, ?, ?, ?, 'success', ?, ?, ?, ?, ?, ?)`,
		b.Name, b.Version, b.System, b.NodejsVersion,
		b.StorePath, b.NarSHA256Hex, b.NarSizeBytes,
		b.FileSHA256Hex, b.FileSizeBytes, b.BuildDurationMS,
	); err != nil {
		return fmt.Errorf("store: record build success: %w", err)
	}
	return nil
}

// RecordBuildFailure replaces the placeholder row with a failed build result.
func (s *Store) RecordBuildFailure(ctx context.Context, b BuildFailure) error {
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.builds
		 (name, version, system, nodejs_version, status, log_excerpt, build_duration_ms)
		 VALUES (?, ?, ?, ?, 'failed', ?, ?)`,
		b.Name, b.Version, b.System, b.NodejsVersion,
		b.LogExcerpt, b.BuildDurationMS,
	); err != nil {
		return fmt.Errorf("store: record build failure: %w", err)
	}
	return nil
}

// GetLatestBuild returns the canonical build record for the given key.
// Returns (zero, false, nil) when no row exists.
func (s *Store) GetLatestBuild(ctx context.Context, name, version, system, nodejs string) (Build, bool, error) {
	var b Build
	err := s.conn.QueryRow(
		ctx,
		`SELECT built_at, name, version, system, nodejs_version, status,
		        store_path, nar_sha256_hex, nar_size_bytes,
		        file_sha256_hex, file_size_bytes, build_duration_ms, log_excerpt
		 FROM skiff.builds FINAL
		 WHERE name = ? AND version = ? AND system = ? AND nodejs_version = ?`,
		name, version, system, nodejs,
	).Scan(
		&b.BuiltAt, &b.Name, &b.Version, &b.System, &b.NodejsVersion, &b.Status,
		&b.StorePath, &b.NarSHA256Hex, &b.NarSizeBytes,
		&b.FileSHA256Hex, &b.FileSizeBytes, &b.BuildDurationMS, &b.LogExcerpt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Build{}, false, nil
	}
	if err != nil {
		return Build{}, false, fmt.Errorf("store: get build: %w", err)
	}
	return b, true, nil
}

// ---------------------------------------------------------------------------
// Cache publications
// ---------------------------------------------------------------------------

// RecordCachePublication inserts or replaces the cache publication for a build key.
func (s *Store) RecordCachePublication(ctx context.Context, p CachePublication) error {
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.cache_publications
		 (name, version, system, nodejs_version,
		  store_path, narinfo_object_key, nar_object_key, cache_url_base)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Version, p.System, p.NodejsVersion,
		p.StorePath, p.NarinfoObjectKey, p.NarObjectKey, p.CacheURLBase,
	); err != nil {
		return fmt.Errorf("store: record cache publication: %w", err)
	}
	return nil
}

// GetLatestCachePublication returns the canonical cache publication for a build key.
// Returns (zero, false, nil) when no row exists.
func (s *Store) GetLatestCachePublication(ctx context.Context, name, version, system, nodejs string) (CachePublication, bool, error) {
	var p CachePublication
	err := s.conn.QueryRow(
		ctx,
		`SELECT name, version, system, nodejs_version,
		        store_path, narinfo_object_key, nar_object_key, cache_url_base
		 FROM skiff.cache_publications FINAL
		 WHERE name = ? AND version = ? AND system = ? AND nodejs_version = ?`,
		name, version, system, nodejs,
	).Scan(
		&p.Name, &p.Version, &p.System, &p.NodejsVersion,
		&p.StorePath, &p.NarinfoObjectKey, &p.NarObjectKey, &p.CacheURLBase,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CachePublication{}, false, nil
	}
	if err != nil {
		return CachePublication{}, false, fmt.Errorf("store: get cache publication: %w", err)
	}
	return p, true, nil
}

// ---------------------------------------------------------------------------
// Observation dedup helper
// ---------------------------------------------------------------------------

// ObservationExists returns true if at least one observation row exists for
// the given (name, version) pair. Used by ingest to skip already-processed
// versions across restarts without races — ClickHouse INSERT is fine to be
// racy; multiple rows for the same key are acceptable in the append-only event
// log. The helper just prevents redundant S3 uploads and re-observations.
func (s *Store) ObservationExists(ctx context.Context, name, version string) (bool, error) {
	var count uint64
	err := s.conn.QueryRow(
		ctx,
		`SELECT count() FROM skiff.package_observations WHERE name = ? AND version = ?`,
		name, version,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("store: observation exists: %w", err)
	}
	return count > 0, nil
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// RecordEvent appends a row to the events log.
func (s *Store) RecordEvent(ctx context.Context, e Event) error {
	payload := e.Payload
	if payload == "" {
		payload = "{}"
	}
	if err := s.conn.Exec(
		ctx,
		`INSERT INTO skiff.events (event_type, name, version, workflow_id, payload)
		 VALUES (?, ?, ?, ?, ?)`,
		e.EventType, e.Name, e.Version, e.WorkflowID, payload,
	); err != nil {
		return fmt.Errorf("store: record event: %w", err)
	}
	return nil
}
