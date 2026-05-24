# Public cache

> **Status:** Placeholder — Phase 1 ships self-host only. A hosted public skiff cache is a Phase 2+ goal.

When the hosted instance launches, this page will document:

- The substituter URL (something like `https://cache.skiff.example/`).
- The Nix binary-cache public key, in the standard `name:base64` format, suitable for adding to `nix.conf` as a trusted key.
- The signing-key rotation policy and how to verify a fresh key.
- The list of npm packages currently mirrored (or, more likely, a pointer at the resolver endpoint that answers that question).

## What this will look like

Once the public instance is live, adding it as a substituter will look like this:

```bash
# in /etc/nix/nix.conf (or ~/.config/nix/nix.conf for user-scoped)
extra-substituters = https://cache.skiff.example/
extra-trusted-public-keys = skiff-public-1:<base64-public-key-here>
```

After that, any Nix build that depends on a derivation already in the skiff cache will fetch it from there instead of rebuilding.

## Until then: self-host

See [`self-host.md`](self-host.md) for running your own skiff cache today. The signing key you generate locally is yours — only you decide who trusts it.

## What the cache attests to

Whether you're consuming a public cache or your own, the signature on each narinfo only attests to a hermetic build: skiff unpacked the integrity-verified npm tarball with no install scripts running. **It does not attest to the safety or correctness of the package source code.** See [`threat-model.md`](threat-model.md) for the full security boundary.
