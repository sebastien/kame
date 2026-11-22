#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — expression statements, computed env and named context
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-20 Kash expressions, environments and execution context"
test-step "build both hosts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
export K07_SECRET='private-value-not-for-traces'
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	check() {
		local expected="$1"; shift
		local invocation=("${runner[@]}")
		local input=()
		if [ "${CACHE_TERMINAL_STDIN:-0}" = 1 ]; then input=(cli_with_terminal_stdin); fi
		if [ "$1" = do ]; then invocation+=("$1" "$2"); shift 2; fi
		if "${input[@]}" timeout 15 "${invocation[@]}" -C "$work" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend evaluation"; else test-fail "$backend evaluation ($*)"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local invocation=("${runner[@]}") status=0
		if [ "$1" = do ]; then invocation+=("$1" "$2"); shift 2; fi
		timeout 15 "${invocation[@]}" -C "$work" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend rejection ($status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend expression statements discard all result kinds and propagate failure"
	check '' do run --allow-run --lang kash -c $'(nop nil)\n(nop {status: 0})\n(run! "true")'
	check effect do run --lang kash -c $'(out! "effect")\n(nop nil)'
	reject USER_ERROR do run --allow-run --lang kash -c $'(fail! "intentional")\nprintf forbidden'
	reject PARSE_ERR do run --allow-run --lang kash -c '(nop "printf") forbidden'
	check computed do run --allow-run --lang kash -c $'def executable = (first ["printf"])\n$executable "%s" "computed"'
	test-step "$backend computed stage environments retain data and ordered overrides"
	check 'record value' do run --allow-run --lang kash -c $'def exports = {MODE: "record value"}\nenv: $exports printenv MODE'
	check 'record' do run --allow-run --lang kash -c 'MODE: pair env: (nop {MODE: "record"}) printenv MODE'
	check 'pair' do run --allow-run --lang kash -c 'env: (nop {MODE: "record"}) MODE: pair printenv MODE'
	check '@(missing) {(missing)} $missing' do run --allow-run --lang kash -c 'def exports = {MODE: """@(missing) {(missing)} $missing"""}; env: $exports printenv MODE'
	check $'line\nbreak' do run --allow-run --lang kash -c 'env: (nop {MODE: "line\nbreak"}) printenv MODE'
	check '' do run --allow-run --lang kash -c 'env: (nop {MODE: ""}) printenv MODE'
	check $'left\nright' do run --allow-run --lang kash -c 'env: (nop {MODE: "left"}) printenv MODE | env: (nop {MODE: "right"}) sh -c "cat; printenv MODE"'
	check 'parent' do run --allow-run --env MODE=parent --lang kash -c $'env: (parse-json "{}") true\nprintenv MODE'
	check 'lowered' do run --allow-run --lang kash -c '(run! :env {MODE: "lowered"} "printenv" "MODE")'
	check safe do run --allow-run --lang kash -c $'if false\n\tenv: (nop {TOKEN: env/K07_SECRET}) printf forbidden\nelse\n\tprintf safe'
	check '' do run --dry-run --allow-run --lang km -c 'task dry' dry --lang kash -c 'env: (nop {TOKEN: env/K07_SECRET}) printf forbidden'
	check '' do run --dry-run --allow-run --lang km -c 'task dry' dry --lang kash -c '(run! :env {TOKEN: env/K07_SECRET} "true")'
	for value in 'nil' '"text"' '["list"]' '{MODE: 1}' '{MODE: nil}' '{MODE: ["nested"]}' '(parse-json "{\"bad=name\":\"secret\"}")' '(parse-json "{\"1BAD\":\"secret\"}")'; do
		reject EXPR_INVALID do run --allow-run --lang kash -c "env: (nop $value) printf forbidden"
	done
	printf '\0' >"$work/nul"
	reject EXPR_INVALID do run --allow-run --allow-read --lang kash -c 'env: (nop {MODE: (text (read ./nul))}) printf forbidden'
	reject PARSE_ERR do run --allow-run --lang kash -c 'env: (nop {}) env: (nop {}) true'
	reject CAP_DENIED do run --no-default-grants --allow-read --lang kash -c 'env: (nop {MODE: "denied"}) printf forbidden'
	test-step "$backend secrets are fetched only at execution and omitted from traces"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
exports = {TOKEN: env/K07_SECRET}
task chosen
	env: $exports sh -c "printf %s \"\$TOKEN\" > received"
task bad
	env: (nop {TOKEN: [env/K07_SECRET]}) true
KM
	if "${runner[@]}" do plan -C "$work" chosen >"$work/plan" 2>"$work/err"; then test-ok "$backend planning defers computed env"; else test-fail "$backend planning evaluated runtime env"; cat "$work/err"; fi
	check '' --dry-run chosen
	if [ ! -e "$work/received" ]; then test-ok "$backend inspection has no effects"; else test-fail "$backend inspection launched secret consumer"; fi
	"${runner[@]}" do run -C "$work" --json --allow-run --allow-env=K07_SECRET "$work/Makefile.km" chosen >"$work/trace" 2>"$work/err"
	if [ "$(cat "$work/received")" = "$K07_SECRET" ] && ! grep -q "$K07_SECRET" "$work/trace" "$work/err"; then test-ok "$backend computed secrets reach only the child"; else test-fail "$backend environment trace leaked data"; fi
	reject EXPR_INVALID do run --allow-run --allow-env=K07_SECRET "$work/Makefile.km" bad
	if ! grep -q "$K07_SECRET" "$work/out" "$work/err"; then test-ok "$backend validation omits evaluated env data"; else test-fail "$backend validation leaked data"; fi
	test-step "$backend env suspension retains effects and tracked values invalidate cache"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
task chosen
	let exports = {TOKEN: text (read ./late)}
	(out! "once")
	env: $exports printenv TOKEN
rule ./late
	printf payload > $outputs.0
KM
	check $'oncepayload' chosen
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
exports = {TOKEN: env/K07_SECRET}
task cached chosen
	env: $exports sh -c "printf x >> runs; printf %s \"\$TOKEN\" > received"
task source-read
	(out! (read ./Makefile.km))
KM
	CACHE_TERMINAL_STDIN=1 check '' do run --no-default-grants --allow-run --allow-env=K07_SECRET "$work/Makefile.km" chosen
	if ! grep -q INPUT_UNSTABLE "$work/err"; then test-ok "$backend validates loaded source without a read grant"; else test-fail "$backend source validation requires user read authority"; fi
	CACHE_TERMINAL_STDIN=1 check '' do run --no-default-grants --allow-run --allow-env=K07_SECRET "$work/Makefile.km" chosen
	if [ "$(cat "$work/runs")" = x ]; then test-ok "$backend computed env permits warm reuse"; else test-fail "$backend computed env reran"; fi
	CACHE_TERMINAL_STDIN=1 K07_SECRET=changed check '' do run --no-default-grants --allow-run --allow-env=K07_SECRET "$work/Makefile.km" chosen
	if [ "$(cat "$work/runs")" = xx ] && [ "$(cat "$work/received")" = changed ]; then test-ok "$backend changed env invalidates task"; else test-fail "$backend stale computed env"; fi
	reject CAP_DENIED do run --no-default-grants --allow-run --allow-env=K07_SECRET "$work/Makefile.km" source-read
	test-step "$backend named operands, function arguments and invalid context"
	check '<one><two words>' do run --allow-run --lang kash -c 'printf "<%s>" $...args' -- one 'two words'
	check 'one-two' do run --allow-run --lang kash -c $'def f one two = join "-" [one, two]\nprintf "%s" (f "one" "two")'
	check '<one><two words>' do run --allow-run --lang kash -c $'def script words...\n\tprintf "<%s>" $...words\n(script "one" "two words")'
	check '"one"' do run --lang expr -c 'args.0' -- one
	check '"shadow"' do run --lang expr -c '(inputs => inputs) "shadow"'
	for source in 'let module/value = 42' 'def module/value = 42' 'def module/function value = value' 'def invalid valid module/value = module/value'; do
		reject PARSE_ERR do run --lang kash -c "$source"
	done
	for name in inputs outputs changed-inputs; do reject SEL_NO_CONTEXT do run --lang kash -c "(nop $name)"; done
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
def selected-files () = changed-inputs
selected = selected-files ()
rule ./output : ./input ./other | prepare
	if inputs != ["./input", "./other"]
		(fail! "inputs differ")
	if outputs != ["./output"]
		(fail! "outputs differ")
	if changed-inputs != inputs
		(fail! "changed inputs differ")
	if selected != changed-inputs
		(fail! "lazy frame differs")
	let names = map basename inputs
	printf "<%s>" $...names > (first outputs)
	let changed = map basename changed-inputs
	printf "<%s>" $...changed > ./changed
task prepare
	(nop args)
task empty
	if inputs != []
		(fail! "task inputs not empty")
	if outputs != ["empty"]
		(fail! "task output name lost")
task invalid
	(nop changed-inputs)
KM
	printf first >"$work/input"
	printf other >"$work/other"
	check '' ./output
	if [ "$(cat "$work/output")" = '<input><other>' ] && [ "$(cat "$work/changed")" = '<input><other>' ]; then test-ok "$backend named frame retains normal input order and outputs"; else test-fail "$backend named rule context"; fi
	check '' empty
	reject SEL_NO_CONTEXT invalid
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
def changed-files () = changed-inputs
rule ./named-output : ./input ./other | prepare
	printf "%s" (join "," (map basename (changed-files ()))) > ./selected
	printf "%s" (join "," (map basename inputs)) > (first outputs)
task prepare
	(nop args)
KM
	check '' ./named-output
	if [ "$(cat "$work/selected")" = input,other ]; then test-ok "$backend named-only selector obtains first-run snapshot"; else test-fail "$backend named-only snapshot"; fi
	printf changed >"$work/input"
	touch -m -d '+2 seconds' "$work/input"
	check '' ./named-output
	if [ "$(cat "$work/selected")" = input ]; then test-ok "$backend named changed inputs exclude unchanged and order-only inputs"; else test-fail "$backend named changed-input selection"; fi
	cat >"$work/Makefile.km" <<'KM'
SHELL = "sh"
rule ./shell-output : ./input
	printf "%s" @{join "," (map basename inputs)} > @{outputs.0}
	printf "%s" @{count args} > ./shell-args
KM
	check '' ./shell-output
	if [ "$(cat "$work/shell-output")" = input ] && [ "$(cat "$work/shell-args")" = 0 ]; then test-ok "$backend external-shell templates share named context and empty invocation args"; else test-fail "$backend external-shell named context"; fi
	test-step "$backend formatting preserves namespaces and typed setup"
	printf '%s\n' 'env: (nop {MODE: "data"}) printf "%s" $...changed-inputs (first outputs) $...args' >"$TMPDIR/source.kash"
	"${runner[@]}" do parse --json --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.ast"
	"${runner[@]}" do fmt --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.fmt"
	"${runner[@]}" do fmt --lang kash "$TMPDIR/$backend.fmt" >"$work/again"
	if cmp -s "$TMPDIR/$backend.fmt" "$work/again"; then test-ok "$backend formatting is idempotent"; else test-fail "$backend formatting"; fi
done
test-step "native/WASM AST and format parity"
for kind in ast fmt; do if cmp -s "$TMPDIR/native.$kind" "$TMPDIR/wasm.$kind"; then test-ok "$kind parity"; else test-fail "$kind parity"; fi; done
test-end
