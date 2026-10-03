#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — wildcard unions retain each glob dependency
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-09 wildcard unions"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project/src/nested" "$project/cmd"
printf a >"$project/src/a.go"
printf b >"$project/src/nested/b.c"
printf excluded >"$project/cmd/excluded.go"
cat >"$project/Makefile.kmk" <<'KMK'
sources = (wildcard ./src/**/*.go ./src/**/*.c ./src/**/*.go ./absent/**/*.h)
./out.txt : @(sources)
	printf '%s\n' @<* > @>
KMK
expected='["./src/a.go" "./src/nested/b.c"]'
for backend in native wasm; do
	test-step "$backend sorted union, deduplication, empty members and operand errors"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	"${runner[@]}" do run --lang expr --allow-read="$project" -C "$project" -c '(wildcard ./src/**/*.go ./src/**/*.c ./src/**/*.go ./absent/**/*.h)' >"$project/value" 2>"$project/error"
	if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend sorted unique union"; else test-fail "$backend union: $(cat "$project/value")"; fi
	status=0
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(wildcard ./src/**/*.go 1)' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q 'argument 2 expects string; got int' "$project/error"; then test-ok "$backend precise invalid member"; else test-fail "$backend operand diagnostic"; fi
	rm -f "$project/out.txt"
	"${runner[@]}" -C "$project" ./out.txt >"$project/value" 2>"$project/error"
	if [ "$(cat "$project/out.txt")" = $'./src/a.go\n./src/nested/b.c' ]; then test-ok "$backend file recipe sees union"; else test-fail "$backend recipe union"; fi
	printf added >"$project/src/nested/new.c"
	"${runner[@]}" -C "$project" ./out.txt >"$project/value" 2>"$project/error"
	if grep -q './src/nested/new.c' "$project/out.txt"; then test-ok "$backend second pattern membership rebuilds output"; else test-fail "$backend stale union member"; fi
	rm "$project/src/nested/new.c"
done
test-end
