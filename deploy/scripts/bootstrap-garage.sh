#!/bin/sh
set -eu
NODE_ID=$(/garage -c /etc/garage.toml status | awk 'NR==3 {print $1}')
# Check whether a layout version has already been applied.
CURRENT_VERSION=$(/garage -c /etc/garage.toml layout show 2>&1 | grep "Current cluster layout version:" | awk '{print $NF}')
if [ -n "$CURRENT_VERSION" ] && [ "$CURRENT_VERSION" -ge 1 ] 2>/dev/null; then
  echo "layout already applied (version $CURRENT_VERSION)"
else
  /garage -c /etc/garage.toml layout assign -z dc1 -c 1G "$NODE_ID"
  /garage -c /etc/garage.toml layout apply --version 1
fi
for bucket in skiff-sources skiff-cache; do
  if ! /garage -c /etc/garage.toml bucket info "$bucket" >/dev/null 2>&1; then
    /garage -c /etc/garage.toml bucket create "$bucket"
  fi
done
KEY_ENV=/shared-scripts/skiff-keys.env
if [ ! -f "$KEY_ENV" ]; then
  KEY_JSON=$(/garage -c /etc/garage.toml key create skiff-dev)
  AK=$(echo "$KEY_JSON" | awk '/Key ID:/ {print $3}')
  SK=$(echo "$KEY_JSON" | awk '/Secret key:/ {print $3}')
  for bucket in skiff-sources skiff-cache; do
    /garage -c /etc/garage.toml bucket allow --read --write --owner --key skiff-dev "$bucket"
  done
  # /shared-scripts is the host's ./scripts directory bind-mounted in.
  # The compose ingest/worker/resolver services read this same file via env_file.
  cat > "$KEY_ENV" <<EOF
S3_ACCESS_KEY_ID=$AK
S3_SECRET_ACCESS_KEY=$SK
EOF
  chmod 644 "$KEY_ENV"
fi
cat "$KEY_ENV"
