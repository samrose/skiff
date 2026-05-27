#!/usr/bin/env bash
# Fail if `go mod tidy` would change go.mod or go.sum.
# Pre-commit invokes hooks from the repo root, so no cd is needed.
set -euo pipefail

# Snapshot the current contents.
before_mod=$(sha256sum go.mod | awk '{print $1}')
before_sum=$(sha256sum go.sum | awk '{print $1}')

nix develop -c go mod tidy

after_mod=$(sha256sum go.mod | awk '{print $1}')
after_sum=$(sha256sum go.sum | awk '{print $1}')

if [[ "$before_mod" != "$after_mod" ]] || [[ "$before_sum" != "$after_sum" ]]; then
  echo "ERROR: go.mod or go.sum is not tidy. 'go mod tidy' changed:" >&2
  if [[ "$before_mod" != "$after_mod" ]]; then
    echo "  - go.mod" >&2
  fi
  if [[ "$before_sum" != "$after_sum" ]]; then
    echo "  - go.sum" >&2
  fi
  echo "Run 'nix develop -c go mod tidy' and commit the result." >&2
  exit 1
fi
