#!/usr/bin/env bash
# Exercise the production Go host, including process ownership and fault paths.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)
export CGO_ENABLED=0
export GOCACHE=${GOCACHE:-/tmp/kame-go-cache}
go -C "$root/src/go/kame" test -count=1 -gcflags=all=-d=checkptr=2 ./host/posix/... ./ownership/... -timeout 90s
