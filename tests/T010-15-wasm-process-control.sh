#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper, process control
# Spec: docs/spec/006-runtime.md — retries and timeouts
# Spec: docs/spec/013-tests.md — T010-15-wasm-process-control
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-15 wasm process timeout and retry"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-process"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF'
./slow.txt :
	sleep 5
	printf slow > slow.txt

./flaky.txt :
	if [ -f marker ]; then printf ok > flaky.txt; else touch marker; false; fi
EOF

test-step "--timeout kills an overrunning recipe"
set +e
(cd "$project" && timeout 20 node "$CLI_ROOT/dist/kame.js" --timeout 300 ./slow.txt) >"$project/out" 2>"$project/err"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'RECIPE_TIMEOUT' "$project/err"; then
	test-ok "timeout reports RECIPE_TIMEOUT with status 1"
else
	test-fail "timeout: status=$status err=$(cat "$project/err")"
fi

test-step "--retry reruns a failed recipe"
rm -f "$project/marker" "$project/flaky.txt"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" --retry 1 ./flaky.txt) >"$project/out" 2>"$project/err"
retry_status=$?
set -e
retry_file="$(cat "$project/flaky.txt" 2>/dev/null)"
rm -f "$project/marker" "$project/flaky.txt"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" ./flaky.txt) >"$project/out2" 2>"$project/err2"
plain_status=$?
set -e
if [ "$retry_status" = 0 ] && [ "$retry_file" = "ok" ] && [ "$plain_status" = 1 ]; then
	test-ok "--retry 1 succeeds where a plain run fails"
else
	test-fail "retry: retry=$retry_status plain=$plain_status file=$retry_file"
fi

test-end
