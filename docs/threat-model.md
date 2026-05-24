# Threat model

This document describes what skiff defends against, what it explicitly does *not* defend against, and what the signature on a published cache entry actually attests to. Read it before adding a skiff cache as a Nix substituter in any environment you care about.

## What the cache signature attests to

Every narinfo skiff publishes carries an ed25519 signature over the standard Nix fingerprint. That signature is a claim, made by the holder of the private key, that:

1. **The artifact bytes match what skiff's worker actually produced.** A narinfo whose `NarHash` doesn't match the referenced `nar.xz` won't pass Nix's own verification, regardless of the signature.
2. **The build was hermetic.** The worker invoked `nix build` against the `packager.nix` derivation with the sandbox enabled. No package-supplied `preinstall`, `install`, or `postinstall` script ran during this build. The output is the tarball's intended-installed contents, minus any lifecycle-script side effects.
3. **The input tarball was integrity-verified.** Before being added to skiff's source store, the tarball's sha512 was checked against the `integrity` field from the npm registry's packument for that exact `(name, version)` pair.

That's it. Those three claims are the entire trust statement.

## What the cache signature does *not* attest to

The signature does **not** mean any of the following:

- **The package source code is safe.** A `pure_js` classification means our rules didn't see install-time lifecycle scripts, native build files, or install-time network fetches. It does *not* mean the JavaScript inside the package is benign. A package can do anything malicious at runtime once you `require` or `import` it — exfiltrate secrets, ship a backdoor, mine cryptocurrency. Skiff doesn't read your package's code and never will.
- **The classifier is a security audit.** The classifier is a filter that decides what we'll try to cache. False negatives are possible and likely. New install-time exfiltration patterns we haven't pattern-matched against can sneak through.
- **The npm publisher is who they claim to be.** Skiff trusts the npm registry. If the registry serves a malicious tarball with a matching integrity hash (because, say, the publisher's account was compromised), skiff dutifully verifies that hash and caches the bad tarball. Mitigations for that threat live upstream of us.
- **Your dependency graph is reviewed.** Just because you can pull `left-pad@1.3.0` from a skiff cache doesn't mean *you* should depend on `left-pad`. Skiff is a build-acceleration substrate, not a curation service.

## Threats skiff is designed to address

| Threat | How skiff helps |
|---|---|
| **Install-time supply-chain attacks** (malicious `preinstall` / `postinstall` scripts running on developer machines and CI) | For `pure_js` packages served from skiff, no install-time script ever ran. Consumers get the same files but without the script execution. |
| **Tarball tampering between npm and the consumer** | Every tarball's sha512 is verified before it lands in skiff's object store; the cache only contains hashed-and-verified content. |
| **Cache pollution / man-in-the-middle on the substituter URL** | Every narinfo is ed25519-signed. Nix clients only accept narinfos signed by a trusted public key listed in `nix.conf`. |
| **Inconsistent build outputs across consumers** | Each build is content-addressed at the Nix store-path level. Two consumers that fetch the same store path get the same bytes. |

## Threats skiff is *not* designed to address

| Threat | Why we don't address it (and what does) |
|---|---|
| **Runtime malicious code inside the package source** | We don't audit source. Use a separate review tool (Socket, Snyk, npm audit, manual code review). |
| **Compromised npm registry serving bad tarballs with matching integrity hashes** | We trust the registry's published integrity hashes. SLSA provenance generation (Phase 2+) would let us cross-check claims; today, npm itself is the root of trust for tarball content. |
| **Compromised skiff infrastructure leaking the signing key** | Operators must secure the key. If it leaks, rotate (see `docs/self-host.md`) and revoke trust in the old public key by removing it from consumer `nix.conf` files. |
| **Novel install-time exfiltration patterns the classifier hasn't seen** | We pattern-match against a known set of suspicious idioms. The set is conservative on purpose, but it isn't exhaustive. Untrusted classification doesn't get built. |
| **Typosquatting attacks (`requrest` vs `request`)** | Out of scope — npm itself is the namespace authority. |
| **Dependency confusion (private package name being shadowed by a public one)** | Out of scope — private registry resolution is your build tooling's job, not skiff's. |

## Operator responsibilities

If you're running a skiff instance:

- Treat the signing private key like any other production secret. Don't check it into source control. Don't email it. Rotate it on a defined cadence.
- Limit who can publish to the cache bucket. Anyone who can write to that bucket can effectively bypass the signing layer for that path (since clients verify the signature, but a malicious operator could re-sign).
- Monitor the `events` table in ClickHouse and the `/metrics` endpoints. Unexpected build activity, sudden classification spikes in `suspicious`, or unfamiliar `(name, version)` pairs are worth investigating.
- Publish your public key in a stable, verifiable location. The whole point of signing is that consumers can pin trust to a key they fetched out of band.

## Consumer responsibilities

If you're adding a skiff cache as a Nix substituter:

- Pin the public key in `nix.conf`. Don't blindly trust a key fetched from the same place as the cache itself (that's circular).
- Still review the packages you depend on. The cache delivers them; it doesn't vet them.
- Understand that hitting the cache doesn't change the *semantics* of the package — `npm install foo` would have given you the same files, with the same runtime behavior, just by a different path. The win is reproducibility, hermeticity, and not running install scripts on your build host.

## Reporting issues

If you find a flaw in the classifier, the signing protocol, or the cache layout that this document doesn't acknowledge, please report it through the channels in [`SECURITY.md`](../SECURITY.md).
