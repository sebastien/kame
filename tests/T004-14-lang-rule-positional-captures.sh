#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — anonymous and positional rule captures
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-14 anonymous and positional rule captures"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

work="$TMPDIR/positional-captures"
project="$work/project"
mkdir -p "$project/src"
cat >"$project/Makefile.kmk" <<'KMK'
./{**}/{*}.c : ./{_0}/{_1}.h
	cat @< > @>
./{*}.name :
	printf '%s' @(_0) > @>
KMK
printf 'positional-capture-ok\n' >"$project/src/demo.h"

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	if (cd "$project" && "${runner[@]}" ./src/demo.c) >"$work/$host.out" 2>"$work/$host.err" && [ "$(cat "$project/src/demo.c")" = "positional-capture-ok" ]; then
		test-ok "$host matched anonymous captures and rendered positional input references"
	else
		test-fail "$host positional capture mapping: $(cat "$work/$host.err")"
	fi
	if (cd "$project" && "${runner[@]}" ./demo.name) >"$work/$host.name.out" 2>"$work/$host.name.err" && [ "$(cat "$project/demo.name")" = "demo" ]; then
		test-ok "$host binds anonymous captures under positional names in recipe expressions"
	else
		test-fail "$host positional capture scope: $(cat "$work/$host.name.err")"
	fi
	if [ "$host" = native ]; then rm -f "$project/src/demo.c"; fi
done

test-end
