#!/usr/bin/env bash
# e2e.sh — Run the Playwright end-to-end suite against a locally built server.
#
# Builds the binary (frontend embedded), starts it on a scratch database with
# its log captured to a file, waits for /health, runs the suite, then tears the
# server down again. Nothing is left behind and no Docker is required.
#
# Usage:
#   ./scripts/e2e.sh                      # run the whole suite
#   ./scripts/e2e.sh app.spec.ts          # run one spec (args go to playwright)
#   make e2e                              # via Makefile
#
# Environment:
#   E2E_PORT                 port to serve on (default 8091)
#   E2E_SKIP_BUILD=1         reuse the existing ./bin/openrsvp
#   E2E_SKIP_BROWSER_INSTALL=1  don't run "playwright install chromium"
#
# To run against a server you started yourself (including docker compose), skip
# this script and drive playwright directly:
#   E2E_BASE_URL=... E2E_SERVER_LOG=... npx playwright test

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${E2E_PORT:-8091}"
ARTIFACT_DIR="${E2E_ARTIFACT_DIR:-$ROOT/tests/e2e/test-results}"

WORKDIR="$(mktemp -d)"
SERVER_LOG="$WORKDIR/server.log"
SERVER_PID=""

cleanup() {
  local status=$?

  # The workdir is about to go away, so keep the server log when something went
  # wrong — on CI it is the only record of what the server was doing.
  if [[ $status -ne 0 && -s "$SERVER_LOG" ]]; then
    mkdir -p "$ARTIFACT_DIR"
    cp "$SERVER_LOG" "$ARTIFACT_DIR/server.log"
    echo "==> Server log saved to $ARTIFACT_DIR/server.log"
  fi

  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "$SERVER_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

# A server left over from an earlier run would silently serve an old build, so
# fail loudly instead of testing the wrong thing.
if curl -fsS -m 2 "http://localhost:$PORT/health" >/dev/null 2>&1; then
  echo "error: something is already serving port $PORT. Stop it, or set E2E_PORT." >&2
  exit 1
fi

# ── 1. Build ─────────────────────────────────────────────────────────────────
if [[ "${E2E_SKIP_BUILD:-}" != "1" ]]; then
  echo "==> Building server (frontend + binary)..."
  make -C "$ROOT" build
fi

BINARY="$ROOT/bin/openrsvp"
if [[ ! -x "$BINARY" ]]; then
  echo "error: $BINARY not found. Run without E2E_SKIP_BUILD=1 to build it." >&2
  exit 1
fi

# ── 2. Start the server ──────────────────────────────────────────────────────
# ENV=development is required: the suite reads magic link tokens out of the log,
# and the server only logs them in development mode.
echo "==> Starting server on port $PORT..."
mkdir -p "$WORKDIR/uploads"
# exec so the subshell is replaced by the server: $! must be the server's own
# PID, otherwise cleanup kills the subshell and leaves the port bound.
(
  cd "$WORKDIR"
  exec env \
    ENV=development \
    PORT="$PORT" \
    DB_DRIVER=sqlite \
    DB_DSN="$WORKDIR/openrsvp.db" \
    UPLOADS_DIR="$WORKDIR/uploads" \
    BASE_URL="http://localhost:$PORT" \
    "$BINARY" >"$SERVER_LOG" 2>&1
) &
SERVER_PID=$!

echo "==> Waiting for http://localhost:$PORT/health ..."
for _ in $(seq 1 60); do
  if curl -fsS -m 2 "http://localhost:$PORT/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "error: server exited during startup. Log:" >&2
    cat "$SERVER_LOG" >&2
    exit 1
  fi
  sleep 0.5
done

if ! curl -fsS -m 2 "http://localhost:$PORT/health" >/dev/null 2>&1; then
  echo "error: server did not become healthy within 30s. Log:" >&2
  cat "$SERVER_LOG" >&2
  exit 1
fi

# ── 3. Run the suite ─────────────────────────────────────────────────────────
cd "$ROOT/tests/e2e"

if [[ ! -d node_modules ]]; then
  echo "==> Installing e2e dependencies..."
  npm ci
fi

if [[ "${E2E_SKIP_BROWSER_INSTALL:-}" != "1" ]]; then
  echo "==> Ensuring the chromium browser is installed..."
  npx playwright install chromium
fi

echo "==> Running Playwright..."
E2E_BASE_URL="http://localhost:$PORT" \
E2E_SERVER_LOG="$SERVER_LOG" \
  npx playwright test "$@"
