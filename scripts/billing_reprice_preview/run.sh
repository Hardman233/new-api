#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
APP_CONTAINER=${NEW_API_CONTAINER:-new-api}
GO_IMAGE=${REPRICE_GO_IMAGE:-golang:1.25.1}
GOPROXY=${GOPROXY:-https://goproxy.cn,direct}
export GOPROXY

# Default range: the complete Beijing-time calendar days 2026-07-15 and
# 2026-07-16. Explicit --start/--before arguments still take precedence.
has_start=0
has_before=0
for arg in "$@"; do
  case "$arg" in
    --start|--start=*) has_start=1 ;;
    --before|--before=*) has_before=1 ;;
  esac
done
[ "$has_start" -eq 1 ] || set -- --start 1784044800 "$@"
[ "$has_before" -eq 1 ] || set -- --before 1784217600 "$@"

timestamp=$(TZ=Asia/Shanghai date '+%Y%m%d-%H%M%S')
filename="reprice-preview-${timestamp}.xlsx"

cd "$ROOT"
mkdir -p billing-reprice-output

if command -v go >/dev/null 2>&1; then
  exec go run ./cmd/billing-reprice-preview --output "$ROOT/billing-reprice-output/$filename" "$@"
fi

command -v docker >/dev/null 2>&1 || {
  echo "error: go or docker is required" >&2
  exit 1
}
docker inspect "$APP_CONTAINER" >/dev/null 2>&1 || {
  echo "error: container '$APP_CONTAINER' not found; set NEW_API_CONTAINER" >&2
  exit 1
}

dsn=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$APP_CONTAINER" | sed -n 's/^SQL_DSN=//p' | head -n 1)
log_dsn=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$APP_CONTAINER" | sed -n 's/^LOG_SQL_DSN=//p' | head -n 1)
network=$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{"\n"}}{{end}}' "$APP_CONTAINER" | head -n 1)
[ -n "$dsn" ] || { echo "error: SQL_DSN not found in $APP_CONTAINER" >&2; exit 1; }
[ -n "$network" ] || { echo "error: Docker network not found" >&2; exit 1; }

if [ -n "$log_dsn" ]; then
  exec docker run --rm --network "$network" \
    -e SQL_DSN="$dsn" -e LOG_SQL_DSN="$log_dsn" -e TZ=Asia/Shanghai -e GOPROXY="$GOPROXY" \
    -v "$ROOT:/src" -v "$ROOT/data:/data" \
    -v billing-reprice-go-cache:/go/pkg/mod -w /src "$GO_IMAGE" \
    go run ./cmd/billing-reprice-preview --output "/src/billing-reprice-output/$filename" "$@"
fi
exec docker run --rm --network "$network" \
  -e SQL_DSN="$dsn" -e TZ=Asia/Shanghai -e GOPROXY="$GOPROXY" \
  -v "$ROOT:/src" -v "$ROOT/data:/data" \
  -v billing-reprice-go-cache:/go/pkg/mod -w /src "$GO_IMAGE" \
  go run ./cmd/billing-reprice-preview --output "/src/billing-reprice-output/$filename" "$@"
