#!/usr/bin/env bash
# Repeat the production Go CLI in one process, including success/error teardown.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)
export CGO_ENABLED=0
export GOCACHE=${GOCACHE:-/tmp/kame-go-cache}
bash "$root/src/sh/generate-version.sh"
go -C "$root/src/go/kame" test -count=1 -gcflags=all=-d=checkptr=2 ./cmd/kame -run '^TestRunLifecycle$' -timeout 90s
