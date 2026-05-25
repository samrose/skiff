# Package Classification

Skiff classifies every npm package version it observes before deciding whether to build it. Classification is performed by the `pkg/classify` package — a pure Go function that reads the tarball stream exactly once and returns one of six classes.

Rules are evaluated in **precedence order**: the first rule that matches wins. A package is `pure_js` only if no other rule matches.

---

## The Six Classes

### 1. `broken` — highest precedence

A package is `broken` when the tarball cannot be read, or its `package.json` is unusable.

Triggers (any one is sufficient):
- The gzip or tar stream is malformed (e.g. a truncated download).
- `package.json` is absent from the tarball.
- `package.json` contains invalid JSON.
- `package.json` is valid JSON but the `name` field is empty or missing.

Reason examples:
- `tarball failed to unpack: gzip: unexpected EOF`
- `package.json missing`
- `package.json invalid JSON: invalid character 'n' looking for beginning of object key string`
- `package.json missing name`

`RuleMatched`: `broken`

**Action:** no build attempt. The classification is stored in ClickHouse for auditing.

---

### 2. `suspicious`

A package is `suspicious` when any of its install-time lifecycle scripts (`preinstall`, `install`, `postinstall`) contains patterns associated with credential theft, arbitrary remote code execution, or secret exfiltration.

Only install-time scripts are checked. Test, build, and other scripts are explicitly excluded.

Triggers:

| Sub-rule | Pattern | `RuleMatched` |
|---|---|---|
| curl pipe sh | `curl <args> \| [sudo] sh` | `suspicious.curl_pipe_sh` |
| wget pipe sh | `wget <args> \| [sudo] sh` | `suspicious.wget_pipe_sh` |
| eval decoded | `eval(atob(...))` or `eval(Buffer.from(...))` | `suspicious.eval_decoded` |
| path secret ref | `/etc/passwd`, `/etc/shadow`, `~/.ssh`, `~/.aws`, `$HOME/.ssh`, `$HOME/.aws` | `suspicious.path_secret_ref` |
| exfil env var | `$AWS_*`, `$GITHUB_TOKEN`, `$GH_TOKEN`, `$NPM_TOKEN`, `$GITLAB_TOKEN`, `$DOCKER_PASSWORD`, `$KUBE_TOKEN` | `suspicious.exfil_env_var` |

Reason includes the script name and a short excerpt of the matching text.

**Action:** no build attempt.

---

### 3. `has_native_code`

A package has native code when the tarball contains compiled or compilable native artifacts.

Triggers (any one is sufficient):
- A file named `binding.gyp` exists at the package root.
- Any file path matches `**/*.{node,c,cc,cpp,cxx,m,mm,h,hpp}`.

`RuleMatched`: `native.binding_gyp` or `native.source_file`.

Reason includes the matching file path.

**Action:** no build attempt in Phase 1. Native builds are a Phase 2+ goal.

Borderline cases:
- A `binding.gyp` reference inside a `README.md` does NOT trigger this rule. Only actual file presence triggers it.
- A `.c` filename in a script string does NOT trigger it. Only tarball entry paths are checked.

---

### 4. `fetches_at_install`

A package fetches at install time when its install scripts reference pre-built binary helper tools or contain literal HTTP/HTTPS URLs (after stripping shell comment lines).

Triggers (any one is sufficient, in `preinstall`, `install`, or `postinstall`):
- String `node-pre-gyp` (sub-string match)
- String `@mapbox/node-pre-gyp`
- String `prebuild-install`
- Literal `http://` or `https://`

Comment stripping: everything after `#` on each script line is removed before matching. This prevents false positives from commented-out URLs.

`RuleMatched`: `fetches.prebuilt_helper` or `fetches.url_literal`.

**Action:** no build attempt in Phase 1.

Borderline cases:
- `https://` in a *test* script does NOT trigger this rule (only install-time scripts are checked).
- `node-pre-gyp` in a *build* script does NOT trigger this rule.
- `https://example.com # see docs` — the URL is after the `#` and is stripped before matching, so it does NOT trigger the rule.

---

### 5. `has_lifecycle_script`

A package has a lifecycle script when `package.json` declares a non-empty `preinstall`, `install`, or `postinstall` value in its `scripts` field.

This is the catch-all for packages that have install hooks but were not flagged by any of the higher-precedence rules (no suspicious patterns, no native code, no remote fetches).

`RuleMatched`: `lifecycle.preinstall`, `lifecycle.install`, or `lifecycle.postinstall`.

Reason: `package.json has lifecycle script "postinstall"`.

**Action:** no build attempt in Phase 1. A lifecycle script could be benign (e.g. `echo done`) but Skiff cannot know without executing it, which would defeat the purpose of the hermetic builder.

Borderline cases:
- A lifecycle script whose value is entirely whitespace is treated as not set (same as absent).
- A package with both `has_lifecycle_script` conditions and `fetches_at_install` conditions is classified as `fetches_at_install` because that rule has higher precedence.

---

### 6. `pure_js` — lowest precedence (default)

A package is `pure_js` when none of the above rules match. It has no install-time scripts, no native code, and no remote fetches.

`RuleMatched`: empty string.

Reason: `no lifecycle scripts, no native files, no install-time fetches`.

**Action:** `pure_js` packages are built with `nix build` in a sandboxed environment. The result is signed and published to the Skiff binary cache.

---

## Classifier Version

Every classification record includes a `classifier_version` field. The current version is `0.1.0`. This field is bumped whenever the rule set changes, allowing analytics to group historical classifications by the rule version that produced them.

---

## Implementation

The classifier lives in `pkg/classify`. It has zero non-stdlib dependencies and can be embedded in any context. The entry point is:

```go
func Classify(tarball io.Reader) (Classification, error)
```

Fixture-based unit tests for every class live in `pkg/classify/rules_test.go`. The fixture tarballs in `pkg/classify/testdata/` can be regenerated by running `pkg/classify/testdata/build-fixtures.sh`.

---

## See Also

- `docs/architecture.md` — full pipeline overview
- `docs/threat-model.md` — what the classifier does and does not guarantee
- `pkg/classify/rules.go` — implementation of the five rules
