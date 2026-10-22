#!/usr/bin/env bash
# Run the acceptance suite (build tag "acceptance") on vps2 with a finite timeout
# and print a PASS/FAIL table. Each test uses its own throwaway cmt-<8hex> server.
# Usage: scripts/acceptance.sh [workspace-id] [timeout-seconds]
set -uo pipefail
WS="${1:-acceptG}"
TMO="${2:-900}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LOG="$(mktemp "${TMPDIR:-/tmp}/mtm-acceptance.XXXXXX")"
"$ROOT/scripts/remote.sh" "$WS" "$TMO" -- 'go test -tags acceptance -count=1 -timeout 12m -v ./test/acceptance/... 2>&1' | tee "$LOG"
rc=${PIPESTATUS[0]}
echo
printf '%-28s %-6s %s\n' TEST RESULT TIME
grep -E '^--- (PASS|FAIL|SKIP): ' "$LOG" | sed -E 's/^--- (PASS|FAIL|SKIP): ([^ ]+) \((.*)\)/\2 \1 \3/' | while read -r n r d; do
  printf '%-28s %-6s %s\n' "$n" "$r" "$d"
done
echo "log: $LOG"
[[ $rc -eq 0 ]] && echo "SUITE PASS" || echo "SUITE FAIL (exit $rc)"
exit "$rc"
