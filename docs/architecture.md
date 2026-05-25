# Skiff Architecture

Skiff is an open-source npm hermetic packager. It watches the public npm registry, classifies new package versions for build safety, and produces deterministic, sandboxed, signed Nix builds that land in a Cachix-compatible binary cache anyone can use as a Nix substituter.

## Pipeline Overview

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
│  - long-poll _changes  │   Temporal: StartWorkflow │  (container, Linux, │
│  - persist last-seq    │ ───────────────────────► │   with Nix daemon,  │
│  - StartWorkflow per   │                          │   privileged for    │
│    (name, version)     │                          │   sandbox)          │
└─────────┬──────────────┘                          │                     │
          │                                         │  ProcessPackage     │
          │ writes events                           │   ├─ FetchAndStore  │
          ▼                                         │   │  (S3 stream)    │
┌────────────────────────────┐                      │   ├─ Classify       │
│ ClickHouse                 │ ◄────── reads/writes │   └─ BuildPackage   │
│  events / observations /   │       events, state  │      ├─ NixBuild    │
│  classifications / builds /│                      │      ├─ SignNarinfo  │
│  cache_publications /      │                      │      └─ UploadCache  │
│  ingest_state              │                      └────────┬────────────┘
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

## Components

| Component | Role |
|---|---|
| `cmd/ingest` | Long-polls npm `_changes` feed; downloads + integrity-verifies tarballs; stores them in Garage; starts a Temporal workflow per `(name, version)`. |
| `cmd/worker` | Temporal worker; runs `ProcessPackage` and `BuildPackage` workflows/activities; invokes Nix inside a sandboxed Linux container; signs narinfo with ed25519; uploads to Garage cache bucket. |
| `cmd/resolver` | HTTP server; answers `GET /resolve/:name/:version` and proxies the Cachix-compatible cache routes so Nix substituters can pull builds. |
| Temporal | Workflow orchestration. One shared task queue `skiff-default`. Backed by PostgreSQL 16 in the local compose stack. |
| ClickHouse | Append-only event log and entity store (observations, classifications, builds, cache publications, ingest checkpoint). |
| Garage | Single-node S3-compatible object store. Two buckets: `skiff-sources` (tarballs) and `skiff-cache` (narinfo + nar.xz). |

## Decisions

| Decision | Value | Rationale |
|---|---|---|
| Object storage backend | Garage in dev *and* prod | One mental model across environments |
| Temporal persistence | PostgreSQL 16 sidecar | `auto-setup` only supports mysql8/postgres12/cassandra; postgres is prod-aligned |
| Temporal layout | Single shared queue `skiff-default`, one worker binary | concurrency: 100 workflow tasks, 50 activities |
| Signing key supply | `CACHE_SIGNING_KEY` (raw) or `CACHE_SIGNING_KEY_FILE` (path) | File path wins if both are set |
| Containerization | All services + all three skiff binaries run as Linux images under docker-compose | `nix develop` on macOS is editor/test-runner only |
| License | MIT | Permissive; bundled `LICENSE` file |
| Module path | `github.com/skiff-build/skiff` | Neutral org |
| Contribution gate | DCO sign-off | `Signed-off-by:` on every commit; enforced by GitHub Action; no CLA |
| Container image registry | `ghcr.io/skiff-build/skiff-{ingest,worker,resolver}` | Built and pushed by GitHub Actions on tag |

## Data Flow Summary

1. **Ingest** long-polls `https://replicate.npmjs.com/_changes?feed=longpoll&since=<seq>`.
2. For each new `(name, version)`, ingest downloads the tarball, verifies its sha512 integrity against the packument, stores it in Garage `skiff-sources`, records a `package_observations` row in ClickHouse, and starts a `ProcessPackage` Temporal workflow.
3. **Worker** runs the workflow: classifies the package (five rules in precedence order; `pure_js` is the only buildable class), invokes `nix build` in a sandboxed Linux container, dumps the store path to a `.nar.xz`, signs a narinfo with an ed25519 key, and uploads both to Garage `skiff-cache`.
4. **Resolver** answers substituter queries. Nix clients add the resolver URL to `nix.conf` as a substituter and pull built packages transparently.

See `docs/threat-model.md` for what the ed25519 signature attests to, and what it does not.

See `docs/classification.md` for the six package classes, their precedence order, and the exact rules that determine whether a package gets built.
