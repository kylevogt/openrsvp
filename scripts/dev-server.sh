#!/usr/bin/env bash
# dev-server.sh — Build and run a local server you can click around in.
#
# This is the "see the change in a browser" counterpart to scripts/e2e.sh: it
# uses the same launch recipe, but keeps the server up and its database on disk
# instead of tearing everything down when a test suite finishes.
#
# The server runs with ENV=development, which is what makes magic-link tokens
# appear in the log — that is the only way to log in without a mail provider
# configured, so keep it.
#
# Usage:
#   ./scripts/dev-server.sh                 # build, start, block until Ctrl-C
#   ./scripts/dev-server.sh --seed          # ...and seed demo events first
#   ./scripts/dev-server.sh --seed --fresh  # ...from an empty database
#   make demo                               # shorthand for --seed
#
# Environment:
#   DEV_PORT             port to serve on (default 8099; e2e.sh uses 8091)
#   DEV_DIR              state directory (default $ROOT/.dev)
#   DEV_SKIP_BUILD=1     reuse the existing ./bin/openrsvp
#
# The log is at $DEV_DIR/server.log. To log in as a seeded host, request a magic
# link from the UI and pull the token out of the log:
#   grep 'magic link generated' .dev/server.log | tail -1
# then open http://localhost:$DEV_PORT/auth/verify?token=<token>

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${DEV_PORT:-8099}"
DEV_DIR="${DEV_DIR:-$ROOT/.dev}"
SERVER_LOG="$DEV_DIR/server.log"
BASE_URL="http://localhost:$PORT"

SEED=0
FRESH=0
for arg in "$@"; do
  case "$arg" in
    --seed)  SEED=1 ;;
    --fresh) FRESH=1 ;;
    # Print the header comment block, so --help cannot drift out of date.
    -h|--help) awk 'NR>1 && /^#/ {sub(/^# ?/, ""); print; next} NR>1 {exit}' "$0"; exit 0 ;;
    *) echo "error: unknown argument '$arg' (try --help)" >&2; exit 1 ;;
  esac
done

SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo
    echo "==> Stopping server..."
    kill "$SERVER_PID" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "$SERVER_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

# A leftover server would silently serve an old build, so fail loudly rather
# than showing you the wrong thing.
if curl -fsS -m 2 "$BASE_URL/health" >/dev/null 2>&1; then
  echo "error: something is already serving port $PORT. Stop it, or set DEV_PORT." >&2
  exit 1
fi

# ── 1. Build ─────────────────────────────────────────────────────────────────
# `make build` compiles the frontend and embeds it in the binary. Without it the
# server still starts, but every page is the bare SPA fallback.
if [[ "${DEV_SKIP_BUILD:-}" != "1" ]]; then
  echo "==> Building server (frontend + binary)..."
  make -C "$ROOT" build
fi

BINARY="$ROOT/bin/openrsvp"
if [[ ! -x "$BINARY" ]]; then
  echo "error: $BINARY not found. Run without DEV_SKIP_BUILD=1 to build it." >&2
  exit 1
fi

# ── 2. Start the server ──────────────────────────────────────────────────────
if [[ $FRESH -eq 1 ]]; then
  echo "==> Wiping $DEV_DIR ..."
  rm -rf "$DEV_DIR"
fi
mkdir -p "$DEV_DIR/uploads"

echo "==> Starting server on $BASE_URL ..."
# exec so $! is the server's own PID: without it cleanup kills the subshell and
# leaves the port bound.
(
  cd "$DEV_DIR"
  exec env \
    ENV=development \
    PORT="$PORT" \
    DB_DRIVER=sqlite \
    DB_DSN="$DEV_DIR/openrsvp.db" \
    UPLOADS_DIR="$DEV_DIR/uploads" \
    BASE_URL="$BASE_URL" \
    "$BINARY" >"$SERVER_LOG" 2>&1
) &
SERVER_PID=$!

for _ in $(seq 1 60); do
  curl -fsS -m 2 "$BASE_URL/health" >/dev/null 2>&1 && break
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "error: server exited during startup. Log:" >&2
    cat "$SERVER_LOG" >&2
    exit 1
  fi
  sleep 0.5
done

if ! curl -fsS -m 2 "$BASE_URL/health" >/dev/null 2>&1; then
  echo "error: server did not become healthy within 30s. Log:" >&2
  cat "$SERVER_LOG" >&2
  exit 1
fi

# ── 3. Seed ──────────────────────────────────────────────────────────────────
if [[ $SEED -eq 1 ]]; then
  echo "==> Seeding demo data..."
  DEMO_BASE_URL="$BASE_URL" DEMO_SERVER_LOG="$SERVER_LOG" \
    node "$ROOT/scripts/seed-demo.mjs"
fi

echo
echo "==> Server ready at $BASE_URL  (log: $SERVER_LOG)"
echo "==> Press Ctrl-C to stop."
wait "$SERVER_PID"
