#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — anonymous and positional rule captures
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-14 anonymous and positional rule captures"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}

work="$TMPDIR/positional-captures"
project="$work/project"
mkdir -p "$project/src" "$project/refs"
cat >"$project/Makefile.km" <<'KAME'
rule ./{**}/{*}.c : ./{_0}/{_1}.h
	cat $inputs.0 > $outputs.0
rule ./{*}.name :
	printf %s $_0 > $outputs.0
rule ./repeat/{name}/{name}/{tail}.out : ./refs/{_0}-{_1}-{_2}.txt
	cat $inputs.0 > $outputs.0
rule ./kash/{*}
	printf %s $captures.0 > $outputs.0
rule ./expr/{*} = captures.0
rule ./foreign/{*} ; {shell: "sh"}
	printf %s '@{captures.0}' > '@{outputs.0}'
KAME
printf 'positional-capture-ok\n' >"$project/src/demo.h"
printf 'named-positions\n' >"$project/refs/a-a-b.txt"

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
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
	if (cd "$project" && "${runner[@]}" ./repeat/a/a/b.out) >"$work/$host.repeat.out" 2>"$work/$host.repeat.err" &&
		[ "$(cat "$project/repeat/a/a/b.out")" = named-positions ]; then
		test-ok "$host counts repeated named captures as separate source positions"
	else
		test-fail "$host repeated capture positions: $(cat "$work/$host.repeat.err")"
	fi
	for body in kash expr foreign; do
		if (cd "$project" && "${runner[@]}" "./$body/sample") >"$work/$host.$body.out" 2>"$work/$host.$body.err" &&
			[ "$(cat "$project/$body/sample")" = sample ]; then
			test-ok "$host $body reads captures.0"
		else
			test-fail "$host $body anonymous capture selector: $(cat "$work/$host.$body.err")"
		fi
	done
	if [ "$host" = native ]; then
		rm -f "$project/src/demo.c" "$project/demo.name" "$project/repeat/a/a/b.out" \
			"$project/kash/sample" "$project/expr/sample" "$project/foreign/sample"
	fi
done

test-end
