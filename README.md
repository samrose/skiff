# Skiff

[![CI](https://github.com/skiff-build/skiff/actions/workflows/ci.yml/badge.svg)](https://github.com/skiff-build/skiff/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## What It Is

Skiff is an open-source npm hermetic packager. It watches the public npm registry, classifies new package versions for safety, and produces deterministic, sandboxed, signed Nix builds in a binary cache that anyone can use as a Nix substituter.

When a new npm package version appears in the registry, Skiff:

1. Downloads the tarball and verifies its sha512 integrity against the registry packument.
2. Classifies the package — only `pure_js` packages (no native code, no install scripts, no network-at-install patterns) proceed to build.
3. Builds the package inside a Nix sandbox in a Linux container — no network, no install scripts, fully reproducible.
4. Signs the resulting Nix store path with an ed25519 key and publishes it to a Cachix-compatible binary cache backed by Garage S3.

Nix users add the cache URL as a substituter. When they install a package that Skiff has built, Nix pulls the pre-built binary instead of building from source.

## Project Status

**Phase 1: streaming-only pipeline, self-host.** The core pipeline is under active development. A public hosted instance is on the Phase 2 roadmap — see [docs/public-cache.md](docs/public-cache.md).

The binary cache signature tells you that *this skiff worker* built the artifact from an integrity-verified tarball with no install scripts. It does not attest to the security or correctness of the source code. Read [docs/threat-model.md](docs/threat-model.md) before deploying.

## Quickstart (Self-Host)

**Requirements:** Docker Desktop (or Colima) + Nix with flakes enabled.

```bash
# 1. Clone the repo
git clone https://github.com/skiff-build/skiff.git
cd skiff

# 2. Enter the dev shell (pins Go 1.26, gopls, golangci-lint, temporal-cli, etc.)
nix develop

# 3. Bring up the full stack (Temporal + ClickHouse + Garage)
docker compose -f deploy/docker-compose.yml up -d

# 4. Wait for all services to be healthy (up to 180 seconds on first pull)
bash deploy/scripts/wait-for-stack.sh

# 5. Verify ClickHouse and Garage are ready
docker compose -f deploy/docker-compose.yml exec clickhouse \
  clickhouse-client --query 'SHOW DATABASES' | grep skiff
docker compose -f deploy/docker-compose.yml logs garage-bootstrap | tail -5
```

The Temporal web UI is available at `http://localhost:8233`.

Once Milestone 5 lands, the ingest, worker, and resolver binaries will be available as container images. Full pipeline quickstart instructions will be added to this section at that point.

**Add the cache as a Nix substituter** (once M5 is deployed):

```nix
# In your NixOS configuration or ~/.config/nix/nix.conf:
substituters = https://cache.nixos.org https://<your-skiff-resolver-host>/cache
trusted-public-keys = cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= skiff-dev-1:<base64-pubkey>
```

Replace `<your-skiff-resolver-host>` and `<base64-pubkey>` with values from your deployment's signing key generation (see [docs/operations.md](docs/operations.md)).

## Future: Public Cache

A hosted Skiff cache is planned for Phase 2+. When it launches, its URL and the substituter public key will be published at [docs/public-cache.md](docs/public-cache.md). You will be able to add it to your Nix configuration without running any infrastructure yourself.

## What Gets Built

Skiff classifies every npm package version against five rules (in precedence order):

1. Broken / malformed tarball
2. Suspicious content patterns
3. Native code (`.node` binaries, `binding.gyp`)
4. Fetches at install time (pre/post-install network calls)
5. Lifecycle scripts (`preinstall`, `install`, `postinstall`)

Only packages that pass all five rules as `pure_js` are built. This is a conservative filter — it is expected to pass a minority of packages at launch. See [docs/classification.md](docs/classification.md) for full details.

## What This Is NOT

Skiff's binary cache signature is a **build attestation**, not a **trust attestation**. It tells you that the artifact bytes were produced by this pipeline from a tarball whose integrity was verified. It does not tell you that the package source is safe, secure, or non-malicious.

Read [docs/threat-model.md](docs/threat-model.md) before adding Skiff as a substituter in a security-sensitive environment.

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Key points:

- Every commit requires a DCO sign-off (`git commit -s`).
- Use conventional commit messages (`feat:`, `fix:`, `docs:`, etc.).
- File bugs and feature requests in [GitHub Issues](https://github.com/skiff-build/skiff/issues).
- Read the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

MIT — see [LICENSE](LICENSE).
