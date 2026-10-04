#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-10 mixed runner dry-run"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/dry-run"
mkdir -p "$work"
cat >"$work/rules.kmk" <<'KMK'
build :
	@(out "rule-out")
	@(err "rule-err")
	@(write "protected" "rule-write")
	touch recipe-ran
KMK
cat >"$work/values.km" <<'KM'
capture = $(touch capture-ran)
legacy = (shell "touch shell-ran")
(out "value-out")
(err "value-err")
(write "protected" "value-write")
(cat capture legacy)
KM
cat >"$work/process.kash" <<'KASH'
printf changed > protected
touch statement-ran
KASH
printf '%s' sentinel >"$work/protected"

cp "$work/rules.kmk" "$work/Makefile.kmk"

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend primary dry-run supports explicit, discovered and inline builds"
	for source in file discover command; do
		arguments=(-n -C "$work")
		if [ "$source" = file ]; then arguments+=(-f rules.kmk build); elif [ "$source" = command ]; then arguments+=(-l kmk -c $'build :\n\ttouch recipe-ran\n' build); else arguments+=(build); fi
		status=0
		"${command[@]}" "${arguments[@]}" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ ! -s "$work/out" ] && [ ! -e "$work/recipe-ran" ] && [ "$(cat "$work/protected")" = sentinel ] && ! grep -q rule-err "$work/err"; then test-ok "$backend primary $source dry-run suppresses effects"; else test-fail "$backend primary $source dry-run"; cat "$work/err"; fi
	done
	"${command[@]}" --json -n -C "$work" build >"$work/primary.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.schema == 1 and .type != "stdout" and .type != "stderr" and .type != "process-started")' "$work/primary.jsonl" >/dev/null; then test-ok "$backend primary JSON dry-run has no live effects"; else test-fail "$backend primary JSON dry-run"; fi

	test-step "$backend suppresses effects in rule, value, lazy and Kash work"
	status=0
	"${command[@]}" do run -n -C "$work" "$work/rules.kmk" build "$work/values.km" "$work/process.kash" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = 0 ] && [ ! -s "$work/$backend.out" ] && ! grep -qE 'rule-err|value-err' "$work/$backend.err"; then test-ok "$backend dry-run emits no output effects or implicit values"; else test-fail "$backend dry-run failed: $status"; cat "$work/$backend.err"; fi
	if [ "$(cat "$work/protected")" = sentinel ] && [ ! -e "$work/recipe-ran" ] && [ ! -e "$work/capture-ran" ] && [ ! -e "$work/shell-ran" ] && [ ! -e "$work/statement-ran" ]; then test-ok "$backend suppresses every process/write path"; else test-fail "$backend performed a dry-run effect"; fi

	test-step "$backend still evaluates pure work and validates commands"
	status=0
	"${command[@]}" do run -n -C "$work" "$work/rules.kmk" build -c '(first 1)' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend does not skip pure expression validation"; else test-fail "$backend skipped pure expression"; fi
	status=0
	"${command[@]}" do run -n -C "$work" "$work/rules.kmk" build -l kash -c 'printf @([bad: 1])' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err"; then test-ok "$backend validates typed argv without launching"; else test-fail "$backend skipped argv validation"; fi
	status=0
	"${command[@]}" do run -n -C "$work" -c '$(touch denied)' "$work/rules.kmk" build >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -e "$work/denied" ]; then test-ok "$backend retains invocation capability restrictions"; else test-fail "$backend changed dry-run grants"; fi

	test-step "$backend single rule and mixed JSON dry-runs"
	"${command[@]}" do run -n -C "$work" "$work/rules.kmk" build >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/recipe-ran" ]; then test-ok "$backend single rule source dry-run"; else test-fail "$backend single source effects"; fi
	"${command[@]}" do run --json -n -C "$work" "$work/rules.kmk" build "$work/values.km" "$work/process.kash" >"$work/$backend.jsonl" 2>"$work/$backend.json.err"
	if [ ! -s "$work/$backend.json.err" ] && jq -e -s 'all(.schema == 1 and .type != "stdout" and .type != "stderr" and .type != "process-started")' "$work/$backend.jsonl" >/dev/null; then test-ok "$backend JSON reports planning without live effects"; else test-fail "$backend JSON dry-run"; fi
done
test-step "portable dry-run event parity"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "dry-run JSON event parity"; else test-fail "dry-run JSON differs"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
test-end
