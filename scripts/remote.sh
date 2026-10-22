#!/usr/bin/env bash
# Remote runner: sync this repo to vps2 into an isolated workspace and run a command there with a finite timeout.
# Usage: scripts/remote.sh <workspace-id> [timeout-seconds] -- <command...>
# Example: scripts/remote.sh agentA 600 -- go build ./... && go test ./...
set -euo pipefail
WS="${1:?workspace id}"; shift
TMO=600
if [[ "${1:-}" =~ ^[0-9]+$ ]]; then TMO="$1"; shift; fi
[[ "${1:-}" == "--" ]] && shift
HOST="${REMOTE_HOST:-vps2}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RDIR="~/work/gmtm-${WS}"
timeout 20 ssh -o BatchMode=yes "$HOST" "mkdir -p $RDIR"
timeout 120 rsync -az --delete --exclude .git --exclude 'bin/' -e "ssh -o BatchMode=yes -o ConnectTimeout=10" "$ROOT/" "$HOST:$RDIR/"
timeout "$((TMO+30))" ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "export PATH=\$HOME/tools/go/bin:\$HOME/go/bin:\$PATH GOFLAGS=-mod=mod; cd $RDIR && timeout --signal=TERM --kill-after=10 ${TMO} bash -lc '$*'"
