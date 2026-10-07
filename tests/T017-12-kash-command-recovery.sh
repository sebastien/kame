#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-12 Kash command recovery"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/recovery"
mkdir -p "$work"

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend: $*"; else test-fail "$backend recovery (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend propagates $code"; else test-fail "$backend rejection (status $status)"; cat "$work/err"; fi
	}
	test-step "$backend recovers completed process failures and skips unused fallback"
	check 'fallbackafter' do run -l kash -c 'false ? printf fallback; printf after'
	check 'ok' do run -l kash -c 'printf ok ? kame-unused-missing-command'
	check 'fallback' do run -l kash -c 'kame-missing-recovery-command ? printf fallback'
	check 'fallback' do run -l kash -c ':timeout 0.05 sleep 30 ? printf fallback'
	check 'fallback' do run -l kash -c 'false | cat ? false ? printf fallback'
	check 'retainedfallback' do run -l kash -c '/bin/sh -c "printf retained; printf warning >&2; exit 7" ? printf fallback'
	if [ "$(cat "$work/err")" = warning ]; then test-ok "$backend preserves live stderr without an unhandled diagnostic"; else test-fail "$backend changed recovered diagnostic output"; fi
	check '"fallback"' do run --allow-run -l expr -c '$(/bin/sh -c "printf discarded; exit 7" ? printf fallback)'
	check '"fallback"' do run --allow-run -l expr -c '$(kame-missing-recovery-command ? false ? printf fallback)'
	check '"fallback"' do run --allow-run -l expr -c $'$(false\n?\n printf fallback)'
	check 'nested' do run -l kash -c 'printf "%s" $(false ? printf nested)'
	check 'accepted' do run -l kash -c 'false ? false?; printf accepted'
	check 'lazy' do run -l kash -c 'printf lazy ? printf @([invalid: 1])'
	check 'fallback' do run -l kash -c 'value = $(false ? printf fallback); printf "%s" $value'
	check 'abab' do run -l kash -c '(recover X) = $(false ? printf "%s" $X); value = @(cat (recover "a") (recover "b") (recover "a") (recover "b")); printf "%s" $value'
	test-step "$backend does not recover argument, authority, capture or fallback failures"
	reject EXPR_INVALID do run -l kash -c 'printf @([invalid: 1]) ? printf forbidden'
	reject CAP_DENIED do run -c '$(false ? printf forbidden)'
	reject RECIPE_FAIL do run -l kash -c 'false ? false'
	reject HOST_FAIL do run -l kash -c 'false ? kame-missing-recovery-command'
	reject CAPTURE_LIMIT do run --allow-run --capture-limit 1 -l expr -c '$(printf too-long ? printf x)'
	reject CAPTURE_ENCODING do run --allow-run -l expr -c '$(printf "\\377" ? printf forbidden)'
	for text in 'false? printf forbidden' 'false ?printf forbidden' 'false ?? printf forbidden' 'false ? | cat'; do reject PARSE_ERR do run -l kash -c "$text"; done
	test-step "$backend suspended fallback never replays the failed command"
	check '"done"' do run --allow-run -C "$work" -l expr -c '$(/bin/sh -c "printf x >> attempts; exit 7" ? printf "%s" $(printf done))'
	if [ "$(cat "$work/attempts")" = x ]; then test-ok "$backend failed branch executed once"; else test-fail "$backend replayed failed process"; fi
	rm "$work/attempts"
	test-step "$backend reports recovered work without an unhandled JSON diagnostic"
	"${command[@]}" do run --json -l kash -c 'false ? printf fallback' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.type != "diagnostic")' "$work/$backend.jsonl" >/dev/null; then test-ok "$backend recovered JSON"; else test-fail "$backend unhandled recovery diagnostic"; fi
done

test-step "portable recovery events, AST and canonical formatting"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt,.elapsedMS)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "recovery event parity"; else test-fail "recovery events differ"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
printf '%s\n' 'false | cat ? false ? printf $(false ? printf fallback)?' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation recovery parity"; else test-fail "$operation recovery parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "recovery formatting is idempotent"; else test-fail "recovery formatting lost binding"; fi
test-end
