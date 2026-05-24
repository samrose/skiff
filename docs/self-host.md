# Self-hosting skiff

This is the operator playbook for running your own skiff instance. Phase 1 scope is single-host docker-compose; Phase 2 will cover production deployment patterns.

## Quickstart (development / evaluation)

1. Clone the repo and enter the dev shell:
   ```bash
   git clone https://github.com/skiff-build/skiff.git
   cd skiff
   nix develop      # optional, for editor tooling — pipeline runs in containers
   ```

2. Generate a binary-cache signing key. (Lands in milestone 5 — see `scripts/generate-signing-key.sh` once it exists.)

3. Bring up the stack:
   ```bash
   docker compose -f deploy/docker-compose.yml up -d
   bash deploy/scripts/wait-for-stack.sh
   ```

4. (Phase 1 milestones M2+) Once the skiff binaries are added as compose services, ingest starts tailing the npm `_changes` feed immediately and a few minutes later you can query the resolver:
   ```bash
   curl -s http://localhost:8081/resolve/left-pad/1.3.0 | jq
   ```

## Operations cheat-sheet

| Action | Command |
|---|---|
| See service health | `docker compose -f deploy/docker-compose.yml ps` |
| Tail worker logs | `docker compose -f deploy/docker-compose.yml logs -f worker` |
| Inspect ClickHouse | `docker compose -f deploy/docker-compose.yml exec clickhouse clickhouse-client --query 'SELECT count(*) FROM skiff.classifications FINAL'` |
| List cached packages in Garage | `docker compose -f deploy/docker-compose.yml exec garage /garage -c /etc/garage.toml bucket info skiff-cache` |
| Reset everything | `docker compose -f deploy/docker-compose.yml down -v` (deletes all volumes — irreversible) |

## Sizing

Phase 1 deployment is a single host running all four stack services plus three skiff binaries. Sufficient for evaluation and small-scale self-hosting. Rough resource minimums on a Linux host:

- CPU: 4 cores (worker spends most of its time in `nix build`)
- RAM: 8 GB (ClickHouse + Temporal + builds)
- Disk: 50 GB for `/nix` store growth, 20 GB for ClickHouse, 100+ GB for Garage source + cache buckets

## What lives where

| Concern | Location | Sensitivity |
|---|---|---|
| Signing private key | `secrets/cache-signing-key` (host-side, mounted into worker) | High — guard it |
| Garage access keys | `deploy/scripts/skiff-keys.env` (generated, gitignored) | Medium — local-only |
| ClickHouse data | docker volume `skiff_clickhouse-data` | Loseable in Phase 1 (event log can be rebuilt) |
| Garage cache contents | docker volumes `skiff_garage-data`, `skiff_garage-meta` | Loseable, but expensive to rebuild |
| Temporal state | docker volume `skiff_temporal-postgres-data` | Loseable in Phase 1 |

## Key rotation

(M5+) Generate a new signing key, point `CACHE_SIGNING_KEY_FILE` at it, restart the worker. The old public key remains valid for already-signed narinfos; clients with both public keys trusted will accept both. Republish the new public key in your `docs/public-cache.md` equivalent.

## Phase 2 topics (not covered yet)

- Multi-host deployment, real PostgreSQL for Temporal, real Garage cluster
- Backup / restore of Garage buckets and ClickHouse
- Monitoring / alerting beyond the `/metrics` endpoints
- TLS termination in front of the resolver
- Public substituter URL with stable DNS
