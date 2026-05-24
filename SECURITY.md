# Security policy

## Supported versions

Skiff is pre-1.0. Only the latest minor release receives fixes; older versions are not patched. Once we reach 1.0 we'll adopt a longer support window and update this section.

## Reporting a vulnerability

Please report security issues privately. Do **not** open a public GitHub issue for a suspected vulnerability.

Two ways to reach us:

1. **GitHub private vulnerability reporting** — open a private report at <https://github.com/skiff-build/skiff/security/advisories/new>. This is the preferred channel.
2. **Email** — `security@skiff-build.example` (TODO: replace with a real address before public launch).

When reporting, please include:

- A description of the issue and its impact
- Steps to reproduce, ideally with a minimal reproduction
- The affected version (commit SHA or release tag)
- Your suggested fix or mitigation, if any

We aim to acknowledge reports within **5 business days** and provide a remediation timeline shortly after triage. We follow a coordinated disclosure model: please do not publish exploit details until a fix is shipped or a mutually agreed embargo expires.

## Sensitive surfaces

Skiff has a few security-relevant surfaces operators and reviewers should pay attention to:

- **The binary-cache signing key.** This is the single most security-sensitive secret in a skiff deployment. A compromise of this key lets an attacker publish narinfos that Nix clients will trust as legitimate skiff output. Operators must guard the key like any other root-of-trust secret. Rotation procedure lives in `docs/self-host.md`.
- **The npm `_changes` consumer.** Untrusted input flows in from the public npm registry. The ingest binary verifies the registry-provided sha512 integrity hash on every tarball before it lands in our object store; everything downstream operates on that verified blob.
- **The Nix build sandbox.** The worker container relies on Nix's sandbox to isolate per-build work. We don't disable sandboxing. See `docs/operations.md` for the container capabilities the worker needs.
- **The classifier rules.** A false negative (e.g., we classify a package as `pure_js` when it secretly has an exfiltration path) means a hermetic build gets published. The signature on that build still only attests to *how we built it* (no install scripts ran), not to the underlying source code's behavior. See `docs/threat-model.md` for the full boundary.

## What this signature does and does not mean

Read `docs/threat-model.md` before deploying skiff in a context where downstream consumers will rely on the cache.
