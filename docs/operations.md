# Skiff Operations Guide

This document covers how to start and manage the local development stack, inspect its state, and rotate secrets.

## Prerequisites

- Docker Desktop (or Colima) with the Docker Compose plugin — the runtime stack runs entirely in Linux containers.
- Nix with flakes enabled — used on the dev host for editor integration, `go test`, and linting only. See `flake.nix`.

> **Key convention:** Every Go command on the dev host runs through the flake-pinned toolchain:
> ```bash
> nix develop -c go build ./...
> nix develop -c golangci-lint run
> ```
> Never use the host's ambient `go` binary — the flake pins Go 1.26, so this ensures every contributor and CI use the same version.

## Starting the Stack

```bash
# From the repo root:
docker compose -f deploy/docker-compose.yml up -d

# Wait for all services to be healthy (exits 0 when ready, 1 on timeout):
bash deploy/scripts/wait-for-stack.sh
```

Services that come up:

| Container | Purpose | Ports |
|---|---|---|
| `skiff-temporal-postgres-1` | PostgreSQL 16 sidecar for Temporal persistence | (internal) |
| `skiff-temporal-1` | Temporal workflow engine (`auto-setup:1.25`) | 7233 (gRPC), 8233 (web UI) |
| `skiff-clickhouse-1` | ClickHouse 24.8 event/entity store | 8123 (HTTP), 9000 (native) |
| `skiff-garage-1` | Garage S3-compatible object store | 3900 (S3 API), 3902 (web) |
| `skiff-garage-bootstrap-1` | One-shot bootstrap (creates buckets + access key) | — |

Temporal auto-setup against Postgres takes ~30 seconds on a cold start. The `wait-for-stack.sh` script polls all five containers (temporal-postgres, temporal, clickhouse, garage) until each reports `healthy` or a 180-second deadline elapses.

## Where Logs Go

```bash
# All services:
docker compose -f deploy/docker-compose.yml logs -f

# Single service:
docker compose -f deploy/docker-compose.yml logs -f temporal
docker compose -f deploy/docker-compose.yml logs -f clickhouse
docker compose -f deploy/docker-compose.yml logs -f garage

# Bootstrap script output (run once on first start):
docker compose -f deploy/docker-compose.yml logs garage-bootstrap
```

## Inspecting ClickHouse

```bash
# Run an arbitrary query:
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query 'SHOW DATABASES'

# Show tables in the skiff database:
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query 'SHOW TABLES FROM skiff'

# Count ingest observations:
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query 'SELECT count() FROM skiff.package_observations'

# Latest events:
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query \
  'SELECT occurred_at, event_type, name, version FROM skiff.events ORDER BY occurred_at DESC LIMIT 20'
```

## Inspecting Garage

```bash
# List buckets:
docker compose -f deploy/docker-compose.yml exec garage /garage bucket list

# List objects in the cache bucket:
docker compose -f deploy/docker-compose.yml exec garage \
  /garage -c /etc/garage.toml bucket info skiff-cache

# Access keys:
docker compose -f deploy/docker-compose.yml exec garage \
  /garage -c /etc/garage.toml key list
```

The bootstrap script writes the access keypair to `deploy/scripts/skiff-keys.env` on the host. This file is gitignored (per-deployment secret). The ingest, worker, and resolver services load it via `env_file:`.

## Resetting State

To wipe all data and start fresh (destroys all volumes):

```bash
docker compose -f deploy/docker-compose.yml down -v
```

This removes all ClickHouse data, Garage objects, Temporal history, and PostgreSQL state. The next `docker compose up -d` reinitializes everything from scratch, including re-running garage-bootstrap to generate a new access keypair.

After a `down -v`, delete the stale keys file before bringing the stack back up:

```bash
rm -f deploy/scripts/skiff-keys.env
docker compose -f deploy/docker-compose.yml up -d
```

## Signing Key

The binary cache signing key is the most security-sensitive secret in a Skiff deployment. It is **not** generated during M0 — that procedure is defined in Milestone 5.

**Placeholder:** The signing key will live at `/run/secrets/cache-signing-key` inside the worker container (or be supplied via `CACHE_SIGNING_KEY` env var). Key generation uses `scripts/generate-signing-key.sh` (M5). Key rotation procedure: generate a new keypair, update the worker's secret, restart the worker, and publish the new public key. Consumers need to add the new public key to their `nix.conf` before rotating away the old one.

**Before public release:** Replace the `conduct@skiff-build.example` placeholder in `CODE_OF_CONDUCT.md` and the `security@skiff-build.example` placeholder in `SECURITY.md` with real mailboxes.

## Temporal Web UI

Browse to `http://localhost:8233` to inspect workflow executions, task queues, and namespaces. Useful for debugging stuck or failed workflows.

## Worker Container Notes

The worker container (`cmd/worker`) requires `cap_add: [SYS_ADMIN]` and `security_opt: ["seccomp:unconfined"]` for the Nix sandbox to work. If these prove brittle in a CI environment, the fallback is `privileged: true`. Document any such change and the security rationale in this file when it is made (Milestone 5).
