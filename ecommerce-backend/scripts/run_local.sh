#!/usr/bin/env bash
# Runs every service natively, without Docker.
# Requires: Go 1.24+, Python 3.10+ (with requirements installed), PostgreSQL 14+ and Redis 6+ already running.
#
#   psql "$DATABASE_URL" -f db/init.sql      # once, to create the schema and seed data
#   scripts/run_local.sh                     # then open http://localhost:8000
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export DATABASE_URL="${DATABASE_URL:-postgres://postgres:postgres@localhost:5432/ecommerce?sslmode=disable}"
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export REDIS_URL="${REDIS_URL:-redis://localhost:6379/0}"
export JWT_SECRET="${JWT_SECRET:-local-dev-secret-change-me}"
export INVOICE_DIR="${INVOICE_DIR:-$ROOT/invoices}"
export FRONTEND_DIR="$ROOT/frontend"

LOG_DIR="$ROOT/logs"
BIN_DIR="$ROOT/services/bin"
mkdir -p "$LOG_DIR" "$BIN_DIR" "$INVOICE_DIR"

echo "Building Go services..."
(cd "$ROOT/services" && for svc in user product inventory order gateway; do
  go build -o "$BIN_DIR/$svc" "./cmd/$svc"
done)

PIDS=()
cleanup() {
  echo; echo "Stopping services..."
  for pid in "${PIDS[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

start() {
  local name="$1"; shift
  "$@" > "$LOG_DIR/$name.log" 2>&1 &
  PIDS+=("$!")
  echo "  started $name (pid $!, log: logs/$name.log)"
}

echo "Starting services..."
start user      "$BIN_DIR/user"
start product   "$BIN_DIR/product"
start inventory "$BIN_DIR/inventory"
start order     "$BIN_DIR/order"
start gateway   "$BIN_DIR/gateway"
(cd "$ROOT/worker" && exec python3 -u main.py > "$LOG_DIR/worker.log" 2>&1) &
PIDS+=("$!")
echo "  started worker (pid $!, log: logs/worker.log)"

sleep 1
echo
echo "ShopSphere is running at http://localhost:8000"
echo "Press Ctrl+C to stop everything."
wait
