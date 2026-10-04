#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — literal wildcard rule inputs
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-11 wildcard inputs"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project/src/nested"
printf a >"$project/src/a.c"
printf b >"$project/src/nested/b.c"
printf literal >"$project/literal*.c"
cat >"$project/Makefile.kmk" <<'KMK'
./out : ./src/**/*.c ./absent/*.c "./literal*.c"
	cat @<* > @>
KMK
for backend in native wasm; do
 test-step "$backend wildcard header inputs track files and membership"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 "${runner[@]}" do parse --lang script "$project/Makefile.kmk" >"$project/ast" 2>"$project/error"
 if grep -q '"kind":"wildcard"' "$project/ast"; then test-ok "$backend marks wildcard inputs"; else test-fail "$backend wildcard AST"; fi
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" >"$project/fmt" 2>"$project/error"
 if cmp -s "$project/Makefile.kmk" "$project/fmt"; then test-ok "$backend preserves authored wildcard format"; else test-fail "$backend wildcard format"; fi
 rm -f "$project/out"
 "${runner[@]}" -C "$project" ./out >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/out")" = abliteral ]; then test-ok "$backend expands recursively and keeps quoted literal"; else test-fail "$backend wildcard inputs"; fi
 printf c >"$project/src/nested/c.c"
 "${runner[@]}" -C "$project" ./out >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/out")" = abcliteral ]; then test-ok "$backend glob membership invalidates output"; else test-fail "$backend stale membership"; fi
 rm "$project/src/nested/c.c"
done
test-end
