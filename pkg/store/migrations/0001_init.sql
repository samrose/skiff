-- /pkg/store/migrations/0001_init.sql
-- Applied by pkg/store/schema.go on worker startup if schema_migrations.version
-- max(version) < 1. Idempotent: CREATE ... IF NOT EXISTS everywhere.

CREATE DATABASE IF NOT EXISTS skiff;

CREATE TABLE IF NOT EXISTS skiff.schema_migrations (
  version     UInt32,
  description String,
  applied_at  DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(applied_at) ORDER BY version;

-- Append-only event log. Source of truth for the pipeline timeline.
CREATE TABLE IF NOT EXISTS skiff.events (
  event_id    UUID            DEFAULT generateUUIDv4(),
  event_type  Enum8(
                'package_observed'   = 1,
                'classified'         = 2,
                'build_started'      = 3,
                'built'              = 4,
                'build_failed'       = 5,
                'cache_published'    = 6,
                'ingest_checkpoint'  = 7,
                'workflow_failed'    = 8
              ),
  occurred_at DateTime64(3, 'UTC') DEFAULT now64(3),
  name        LowCardinality(String) DEFAULT '',
  version     String                 DEFAULT '',
  workflow_id String                 DEFAULT '',
  payload     String DEFAULT '{}'  CODEC(ZSTD(3))
) ENGINE = MergeTree
ORDER BY (occurred_at, event_type, name, version)
PARTITION BY toYYYYMM(occurred_at)
TTL toDateTime(occurred_at) + INTERVAL 90 DAY;

-- One row per change-feed observation. Append-only.
CREATE TABLE IF NOT EXISTS skiff.package_observations (
  observed_at         DateTime64(3, 'UTC') DEFAULT now64(3),
  name                LowCardinality(String),
  version             String,
  registry_seq        UInt64,
  packument_rev       String,
  tarball_url         String,
  tarball_sha512_hex  FixedString(128),
  tarball_size_bytes  UInt64,
  source_object_key   String
) ENGINE = MergeTree
ORDER BY (name, version, observed_at);

-- Latest classification per (name, version). Use FINAL on read.
CREATE TABLE IF NOT EXISTS skiff.classifications (
  classified_at       DateTime64(3, 'UTC') DEFAULT now64(3),
  name                LowCardinality(String),
  version             String,
  classification      Enum8(
                        'broken'               = 1,
                        'suspicious'           = 2,
                        'has_native_code'      = 3,
                        'fetches_at_install'   = 4,
                        'has_lifecycle_script' = 5,
                        'pure_js'              = 6
                      ),
  reason              String,
  rule_matched        LowCardinality(String),
  classifier_version  LowCardinality(String)
) ENGINE = ReplacingMergeTree(classified_at)
ORDER BY (name, version);

-- Latest build per (name, version, system, nodejs_version). Use FINAL on read.
CREATE TABLE IF NOT EXISTS skiff.builds (
  built_at             DateTime64(3, 'UTC') DEFAULT now64(3),
  name                 LowCardinality(String),
  version              String,
  system               LowCardinality(String),
  nodejs_version       LowCardinality(String),
  status               Enum8('success' = 1, 'failed' = 2),
  store_path           String                DEFAULT '',
  nar_sha256_hex       FixedString(64)       DEFAULT '0000000000000000000000000000000000000000000000000000000000000000',
  nar_size_bytes       UInt64                DEFAULT 0,
  file_sha256_hex      FixedString(64)       DEFAULT '0000000000000000000000000000000000000000000000000000000000000000',
  file_size_bytes      UInt64                DEFAULT 0,
  build_duration_ms    UInt32                DEFAULT 0,
  log_excerpt          String DEFAULT '' CODEC(ZSTD(3))
) ENGINE = ReplacingMergeTree(built_at)
ORDER BY (name, version, system, nodejs_version);

-- Latest cache publish per (name, version, system, nodejs_version). Use FINAL.
CREATE TABLE IF NOT EXISTS skiff.cache_publications (
  published_at         DateTime64(3, 'UTC') DEFAULT now64(3),
  name                 LowCardinality(String),
  version              String,
  system               LowCardinality(String),
  nodejs_version       LowCardinality(String),
  store_path           String,
  narinfo_object_key   String,
  nar_object_key       String,
  cache_url_base       String
) ENGINE = ReplacingMergeTree(published_at)
ORDER BY (name, version, system, nodejs_version);

-- Tiny key-value table for ingest checkpoint(s). Single-row writes per key.
CREATE TABLE IF NOT EXISTS skiff.ingest_state (
  key        String,
  value      String,
  updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY key;
