# Contributing to Skiff

Thank you for your interest in contributing! This document covers how to set up your development environment, coding conventions, and the contribution process.

## Code of Conduct

By participating in this project you agree to abide by the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). Please report any concerns to `conduct@skiff-build.example`.

## Development Environment

### Prerequisites

- **Nix** with flakes enabled — provides the pinned Go toolchain, linter, language server, and CLI tools.
- **Docker Desktop** or **Colima** — the pipeline runtime lives entirely in Linux containers; Nix on the dev host is for editing, testing, and linting only.

### Setup

```bash
# 1. Clone the repo
git clone https://github.com/skiff-build/skiff.git
cd skiff

# 2. Enter the dev shell (pins Go 1.26 + all tooling)
nix develop

# 3. Bring up the stack
docker compose -f deploy/docker-compose.yml up -d
bash deploy/scripts/wait-for-stack.sh
```

All Go commands on the dev host use the flake-pinned toolchain:

```bash
nix develop -c go build ./...
nix develop -c go test ./...
nix develop -c golangci-lint run
```

Never use the host's ambient `go` binary — the flake pins Go 1.26 to keep versions consistent across contributors and CI.

### Resetting the Stack

```bash
docker compose -f deploy/docker-compose.yml down -v
rm -f deploy/scripts/skiff-keys.env
docker compose -f deploy/docker-compose.yml up -d
```

## DCO Sign-Off (Required)

Every commit must include a `Signed-off-by` trailer certifying that you have the right to submit the contribution under the project's MIT license. This is the [Developer Certificate of Origin (DCO)](https://developercertificate.org/).

```bash
git commit -s -m "feat: your change"
# Equivalent to adding: Signed-off-by: Your Name <you@example.com>
```

The DCO check runs as a GitHub Actions job on every pull request and will block merging if any commit is missing the trailer.

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

| Type | When to use |
|---|---|
| `feat:` | A new feature |
| `fix:` | A bug fix |
| `docs:` | Documentation only |
| `refactor:` | Code change that is neither a fix nor a feature |
| `test:` | Adding or correcting tests |
| `chore:` | Build tooling, dependency bumps, CI changes |
| `perf:` | Performance improvement |

Examples:

```
feat: add sha512 streaming integrity verification for tarballs
fix: handle empty _changes result without resetting sequence
docs: clarify threat model around classifier false negatives
```

Keep the subject line under 72 characters. Add a body paragraph for anything non-obvious.

## Code Style

- **Formatting:** `go fmt` (enforced by the linter). Run `nix develop -c go fmt ./...` before committing.
- **Linting:** `golangci-lint run` must pass with zero warnings. The configuration is in `.golangci.yml`.
- **Line length:** Advisory 120 characters; the linter does not enforce this strictly, but reviewers may ask for splits on very long lines.
- **Error handling:** Return errors; never swallow them silently. Wrap with `fmt.Errorf("context: %w", err)`.
- **Logging:** Use `log/slog` with structured fields. No `fmt.Println` in library code.
- **Tests:** New features require unit tests. Integration tests (using the live compose stack) should use the `SKIFF_INTEGRATION=1` guard so they don't run in standard `go test ./...`.

## Running Tests

```bash
# Unit tests only (no Docker required):
nix develop -c go test ./...

# Integration tests (requires the compose stack to be up):
SKIFF_INTEGRATION=1 nix develop -c go test ./pkg/store -v
```

CI runs both passes on every pull request. A green CI run is required before merge.

## Pull Request Process

1. Fork the repo and create a branch from `main`.
2. Make your changes, write tests, and ensure `golangci-lint run` passes.
3. Commit with `-s` for DCO sign-off.
4. Open a pull request against `main`. Fill in the PR template checkboxes.
5. Address review feedback. Maintainers aim to respond within 5 business days.
6. Once approved and CI is green, a maintainer will merge.

## Filing Bugs and Feature Requests

Use [GitHub Issues](https://github.com/skiff-build/skiff/issues). Choose the appropriate template:

- **Bug report:** Include reproduction steps, expected vs. actual behavior, and your environment (OS, Docker version, `nix develop -c go version` output).
- **Feature request:** Describe the use case and any alternatives you considered.

For security vulnerabilities, **do not open a public issue**. See [SECURITY.md](SECURITY.md).
