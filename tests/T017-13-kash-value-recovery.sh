#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-13 Kash value recovery"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/value-recovery"
mkdir -p "$work"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1" source="$2"; shift 2
		local status=0
		"${command[@]}" do run -C "$work" "$@" -l kash -c "$source; printf \"%s\" \$value" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend value recovery"; else test-fail "$backend recovery (status $status): $source"; cat "$work/err"; fi
	}
	reject() {
		local code="$1" source="$2"; shift 2
		local status=0
		"${command[@]}" do run -C "$work" "$@" -l kash -c "$source; printf \"%s\" \$value" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend preserves $code"; else test-fail "$backend recovery boundary: $source"; cat "$work/err"; fi
	}
	test-step "$backend selects nil/failure, not false or empty values"
	check fallback 'value = :nil ?? "fallback"'
	check fallback 'value = unknown ?? "fallback"'
	check false 'value = :false ?? "fallback"'
	check 0 'value = 0 ?? "fallback"'
	check '' 'value = "" ?? "fallback"'
	check good 'value = "good" ?? $(touch forbidden)'
	check fallback 'value = :nil ?? missing ?? "fallback"'
	check fallback 'value = $(false) ?? "fallback"'
	check '' 'value = $(false?) ?? "fallback"'
	check command 'value = $(false ? printf command) ?? "fallback"'
	check fallback 'value = $(kame-missing-value-command) ?? "fallback"'
	check fallback 'value = $(:timeout 0.05 sleep 30) ?? "fallback"'
	check x 'value = $(printf too-long) ?? "x"' --capture-limit 1
	check fallback 'value = $(printf "\\377") ?? "fallback"'
	check fallback 'value = (read "missing-file") ?? "fallback"'
	test-step "$backend observes failed lazy definitions without failing the caller"
	check fallback 'broken = $(false); value = broken ?? "fallback"'
	check fallback 'broken = missing; value = broken ?? "fallback"'
	check fallback 'broken = (read "missing-file"); value = broken ?? "fallback"'
	check abab '(recover X) = $(false) ?? X; value = @(cat (recover "a") (recover "b") (recover "a") (recover "b"))'
	test-step "$backend preserves validation and capability failures"
	reject EXPR_INVALID 'value = (first 1) ?? "forbidden"'
	reject EXPR_INVALID 'value = $(printf @([bad: 1])) ?? "forbidden"'
	reject CAP_DENIED 'value = $(false) ?? "forbidden"' --allow-run=/bin/printf
	reject CAP_DENIED 'broken = $(false); value = broken ?? "forbidden"' --allow-run=/bin/printf
	reject REF_MISSING 'value = :nil ?? missing'
	reject RECIPE_FAIL 'broken = $(false); value = broken ?? broken'
	reject DEP_CYCLE 'value = value ?? "forbidden"'
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend skipped fallback effects"; else test-fail "$backend ran unselected fallback"; fi
	test-step "$backend selected async fallback never replays failed left"
	check done 'value = $(/bin/sh -c "printf x >> attempts; exit 7") ?? $(printf done)'
	if [ "$(cat "$work/attempts")" = x ]; then test-ok "$backend replay-safe value recovery"; else test-fail "$backend replayed failed left"; fi
	rm "$work/attempts"
	test-step "$backend JSON reports the recovered value, not an unhandled failure"
	"${command[@]}" do run --json -C "$work" -l kash -c 'broken = $(false); value = broken ?? "fallback"' -l km -c 'value' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.type != "diagnostic") and any(.type == "target-value" and .value.data == "fallback")' "$work/$backend.jsonl" >/dev/null; then test-ok "$backend recovered JSON value"; else test-fail "$backend recovery JSON"; fi
done
test-step "portable recovered value events"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "recovery JSON parity"; else test-fail "recovery JSON differs"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
test-step "value recovery AST and formatting parity"
printf '%s\n' 'value = $(false) ?? missing ?? "fallback"' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "formatting is idempotent"; else test-fail "formatting changed recovery binding"; fi
test-end
