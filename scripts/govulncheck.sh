#!/usr/bin/env bash
# Wrapper around govulncheck that skips IDs listed in .govulncheck-ignore.
# Official govulncheck has no ignore file (golang/go#59507); this keeps the
# gate blocking for everything else.
#
# Usage: ./scripts/govulncheck.sh [govulncheck args...]
# Defaults to scanning ./...

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IGNORE_FILE="$ROOT/.govulncheck-ignore"
cd "$ROOT"

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

set +e
govulncheck -json "$@" >"$tmp"
status=$?
set -e

# 0 = clean, 3 = vulnerabilities found. Anything else is a tool failure.
if [ "$status" -ne 0 ] && [ "$status" -ne 3 ]; then
	echo "govulncheck failed with exit $status" >&2
	cat "$tmp" >&2
	exit "$status"
fi

python3 - "$IGNORE_FILE" "$tmp" <<'PY'
import json, sys

ignore_path, report_path = sys.argv[1], sys.argv[2]

ignore = set()
with open(ignore_path) as f:
    for line in f:
        line = line.strip()
        if line.startswith("GO-"):
            ignore.add(line.split()[0])

called = set()
with open(report_path) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        obj = json.loads(line)
        finding = obj.get("finding")
        if not finding:
            continue
        # Same rule govulncheck uses for "called" findings: the first frame
        # of the trace names a function in our code (or a dependency we call).
        trace = finding.get("trace") or []
        if not trace or not trace[0].get("function"):
            continue
        osv = finding.get("osv")
        if osv:
            called.add(osv)

skipped = sorted(called & ignore)
remaining = sorted(called - ignore)

if skipped:
    print("Ignored (see .govulncheck-ignore):")
    for vid in skipped:
        print(f"  {vid}")
    print()

if remaining:
    print("Vulnerable symbols found:")
    for vid in remaining:
        print(f"  {vid}")
    print("\nRe-run `govulncheck ./...` for traces.")
    sys.exit(1)

if not called:
    print("No vulnerabilities found.")
else:
    print("No un-ignored vulnerabilities found.")
PY
