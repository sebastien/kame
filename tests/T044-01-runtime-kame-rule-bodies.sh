#!/usr/bin/env bash
# Spec: docs/spec/044-value-producing-rule-bodies.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T044-01 value-producing rule bodies"
cd "$CLI_ROOT"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi

for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	printf 'Ada\n' >"$work/name"
	printf 'binary\000\377\n' >"$work/binary"
	cat >"$work/Kamefile" <<'KAME'
task default : ./greeting ./bytes ./empty ./page
rule ./greeting : ./name = str "Hello, " (strip (text (read (first inputs)))) "!\n"
rule ./bytes : ./binary
	return read (first inputs)
rule ./empty : ./name = ""
rule ./page : ./greeting = render {message: strip (text (read (first inputs)))} "plain" ./page.txt
KAME
	printf 'Message: @{message}\n' >"$work/page.txt"
	test-step "$backend publishes exact bytes without processes"
	"${runner[@]}" --json -n -C "$work" --allow-read --allow-write >"$work/dry.json" 2>"$work/err"
	if [ ! -e "$work/greeting" ] && jq -e -s 'all(.type != "process-started")' "$work/dry.json" >/dev/null; then test-ok "$backend dry-run does not publish or execute"; else test-fail "$backend dry-run performed work"; fi
	"${runner[@]}" --json -C "$work" --allow-read --allow-write >"$work/events.json" 2>"$work/err"
	printf 'Hello, Ada!\n' >"$work/expected"
	if cmp -s "$work/greeting" "$work/expected" && cmp -s "$work/bytes" "$work/binary" && [ -f "$work/empty" ] && [ ! -s "$work/empty" ] && [ "$(cat "$work/page")" = 'Message: Hello, Ada!' ] && jq -e -s 'all(.type != "process-started")' "$work/events.json" >/dev/null; then test-ok "$backend value bodies publish text, binary, empty and rendered content"; else test-fail "$backend artifact bytes or interpreter selection"; cat "$work/err"; fi
	"${runner[@]}" --json -C "$work" --allow-read --allow-write ./greeting >"$work/reuse.json" 2>"$work/err"
	if jq -e -s 'any(.type == "target-completed") and all(.type != "effect" and .type != "process-started")' "$work/reuse.json" >/dev/null; then test-ok "$backend reuses unchanged content"; else test-fail "$backend failed to reuse content"; fi
	printf 'Lin\n' >"$work/name"
	"${runner[@]}" -C "$work" --allow-read --allow-write ./page >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/page")" = 'Message: Hello, Lin!' ]; then test-ok "$backend tracks reads"; else test-fail "$backend dependency invalidation"; fi
	cat >"$work/Kamefile" <<'KAME'
SHELL = "/bin/sh"
rule ./shell : ./name
	printf '%s' @{strip (text (read (first inputs)))} > @{first outputs}
KAME
	"${runner[@]}" -C "$work" --allow-read --allow-write --allow-run ./shell >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/shell")" = Lin ]; then test-ok "$backend retains pure shell interpolation"; else test-fail "$backend shell interpolation"; fi

	test-step "$backend distinguishes returned content from normal completion and exit"
	cat >"$work/Kamefile" <<'KAME'
rule ./replacement
	(write! ./replacement "written")
	return "returned\n"
rule ./bare
	(write! ./bare "bare")
	return
rule ./nil
	(write! ./nil "nil")
	return nil
rule ./exited
	(write! ./exited "written")
	exit 0
	return "unreachable"
rule ./missing
	exit 0
	return "unreachable"
rule ./protected : ./name
	(write! ./protected "must remain unchanged")
	(fail! "intentional failure")
	return "unreachable"
rule ./first ./second
	return "ambiguous"
rule ./invalid
	return 42
task ineligible
	return "invalid"
KAME
	"${runner[@]}" -C "$work" --allow-read --allow-write ./replacement ./bare ./nil ./exited >"$work/out" 2>"$work/err"
	printf 'returned\n' >"$work/expected"
	if cmp -s "$work/replacement" "$work/expected" && [ "$(cat "$work/bare")" = bare ] &&
		[ "$(cat "$work/nil")" = nil ] && [ "$(cat "$work/exited")" = written ]; then
		test-ok "$backend publishes returned bytes and keeps ordinary writes"
	else
		test-fail "$backend return or exit replaced the wrong output"
	fi
	printf old >"$work/protected"
	printf 'New input\n' >"$work/name"
	for requirement in './missing:OUTPUT_MISSING' './protected:USER_ERROR' './first:EXPR_INVALID' './invalid:EXPR_INVALID' 'ineligible:EXPR_INVALID'; do
		target="${requirement%%:*}"
		code="${requirement#*:}"
		status=0
		"${runner[@]}" -C "$work" --allow-read --allow-write "$target" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then
			test-ok "$backend $target fails with $code"
		else
			test-fail "$backend $target did not report $code"
			cat "$work/err"
		fi
	done
	if [ "$(cat "$work/protected")" = old ] && [ ! -e "$work/missing" ] && [ ! -e "$work/first" ] &&
		[ ! -e "$work/second" ] && [ ! -e "$work/invalid" ]; then
		test-ok "$backend failure preserves existing output bytes"
	else
		test-fail "$backend published an invalid or failed result"
	fi

	test-step "$backend rejects effects directly and through lazy definitions"
	for body in 'out! "bad"' 'err! "bad"' 'write! ./side "bad"' '$(touch side)' 'sh! "touch side"' 'now ()' 'LAZY' 'helper ()'; do
		for context in expression template; do
			printf 'LAZY = sh! "touch side"\ndef helper () = out! "bad"\n' >"$work/Kamefile"
			if [ "$context" = expression ]; then
				printf 'rule ./greeting : ./name = %s\n' "$body" >>"$work/Kamefile"
			else
				printf 'SHELL = "/bin/sh"\nrule ./greeting : ./name\n\tprintf "%%s" @{%s} > @{first outputs}\n' "$body" >>"$work/Kamefile"
			fi
			status=0
			"${runner[@]}" -C "$work" --allow-read --allow-write --allow-run ./greeting >"$work/out" 2>"$work/err" || status=$?
			if [ "$status" = 1 ] && grep -q PHASE_INVALID "$work/err" && [ ! -e "$work/side" ] && [ "$(cat "$work/greeting")" = 'Hello, Lin!' ]; then
				test-ok "$backend $context rejects $body without replacing the artifact"
			else
				test-fail "$backend $context accepted effect $body"
				cat "$work/err"
			fi
		done
	done
done
test-end
