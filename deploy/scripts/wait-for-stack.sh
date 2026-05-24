#!/usr/bin/env bash
set -euo pipefail
deadline=$(( $(date +%s) + 180 ))
services=(temporal-postgres temporal clickhouse garage)
while :; do
  unhealthy=0
  for s in "${services[@]}"; do
    status=$(docker inspect --format='{{.State.Health.Status}}' "skiff-${s}-1" 2>/dev/null || echo missing)
    [ "$status" = "healthy" ] || unhealthy=1
  done
  [ $unhealthy -eq 0 ] && exit 0
  [ "$(date +%s)" -gt $deadline ] && { echo "stack failed to come up"; docker compose -f deploy/docker-compose.yml ps; exit 1; }
  sleep 2
done
