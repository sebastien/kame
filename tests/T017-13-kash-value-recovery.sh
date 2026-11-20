#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-13 Kash value recovery"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
work="$TMPDIR/value-recovery"
mkdir -p "$work"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$wasm_wrapper"); fi
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
	test-step "$backend falls back only for nil and missing lookups"
	check fallback 'let value = nil ?? "fallback"'
	check fallback 'let value = unknown ?? "fallback"'
	check false 'let value = false ?? "fallback"'
	check 0 'let value = 0 ?? "fallback"'
	check '' 'let value = "" ?? "fallback"'
	check good 'let value = "good" ?? $(touch forbidden)'
	check fallback 'let value = nil ?? missing ?? "fallback"'
	check '' 'let value = $(false ?) ?? "fallback"'
	check command 'let value = $(false ? printf command) ?? "fallback"'
	check fallback 'let value = $(kame-missing-value-command) ?? "fallback"'
	check fallback 'let value = {other: 1}.missing ?? "fallback"'
	check fallback 'let value = env/KAME_ABSENT_RECOVERY_PROBE ?? "fallback"' --allow-env=KAME_ABSENT_RECOVERY_PROBE --allow-run
	test-step "$backend missing lazy lookups recover across suspension"
	check fallback 'def broken = missing; let value = broken ?? "fallback"'
	check abab 'def recover fallback = $(kame-missing-value-command) ?? fallback; let value = str (recover "a") (recover "b") (recover "a") (recover "b")'
	test-step "$backend preserves process, validation and capability failures"
	reject RECIPE_FAIL 'let value = $(false) ?? "forbidden"'
	reject RECIPE_TIMEOUT 'let value = $(timeout: 0.05 sleep 30) ?? "forbidden"'
	reject CAPTURE_LIMIT 'let value = $(printf too-long) ?? "forbidden"' --capture-limit 1
	reject CAPTURE_ENCODING 'let value = $(printf "\\377") ?? "forbidden"'
	reject FS_ERR 'let value = (read ./missing-file) ?? "forbidden"'
	reject USER_ERROR 'let value = (fail! "stop") ?? (out! "fallback")'
	reject EXPR_INVALID 'let value = (first 1) ?? "forbidden"'
	reject EXPR_INVALID 'let value = $(printf (nop {bad: 1})) ?? "forbidden"'
	reject CAP_DENIED 'let value = $(false) ?? "forbidden"' --allow-run=/bin/printf
	reject CAP_DENIED 'def broken = $(false); let value = broken ?? "forbidden"' --allow-run=/bin/printf
	reject REF_MISSING 'let value = nil ?? missing'
	reject RECIPE_FAIL 'def broken = $(false); let value = broken ?? broken'
	reject DEP_CYCLE 'def value = value ?? "forbidden"'
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend skipped fallback effects"; else test-fail "$backend ran unselected fallback"; fi
	test-step "$backend selected async fallback never replays completed left"
	check done 'let value = (nop $(/bin/sh -c "printf x >> attempts") nil) ?? $(printf done)'
	if [ "$(cat "$work/attempts")" = x ]; then test-ok "$backend replay-safe value recovery"; else test-fail "$backend replayed failed left"; fi
	rm "$work/attempts"
	test-step "$backend JSON reports the recovered value, not an unhandled failure"
	"${command[@]}" do run --json --print-result -C "$work" -l kash -c 'def broken = missing; def value = broken ?? "fallback"' -l km -c 'value' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.type != "diagnostic") and any(.type == "target-value" and .value.data == "fallback")' "$work/$backend.jsonl" >/dev/null; then test-ok "$backend recovered JSON value"; else test-fail "$backend recovery JSON"; fi
done
test-step "portable recovered value events"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt,.elapsedMS)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "recovery JSON parity"; else test-fail "recovery JSON differs"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
test-step "value recovery AST and formatting parity"
printf '%s\n' 'let value = nil ?? missing ?? "fallback"' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$wasm_wrapper" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "formatting is idempotent"; else test-fail "formatting changed recovery binding"; fi
test-end
