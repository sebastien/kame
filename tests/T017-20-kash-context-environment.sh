#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — expression statements, computed env and named context
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-20 Kash expressions, environments and execution context"
test-step "build both hosts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
export K07_SECRET='private-value-not-for-traces'
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local invocation=("${runner[@]}")
		if [ "$1" = do ]; then invocation+=("$1" "$2"); shift 2; fi
		if timeout 15 "${invocation[@]}" -C "$work" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend evaluation"; else test-fail "$backend evaluation"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local invocation=("${runner[@]}") status=0
		if [ "$1" = do ]; then invocation+=("$1" "$2"); shift 2; fi
		timeout 15 "${invocation[@]}" -C "$work" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend rejection ($status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend expression statements discard all result kinds and propagate failure"
	check '' do run --allow-run --lang kash -c $'(nop :nil)\n(nop [status: 0])\n(run! "true")'
	check effect do run --lang kash -c $'(out! "effect")\n(nop :nil)'
	reject USER_ERROR do run --allow-run --lang kash -c $'(fail! "intentional")\nprintf forbidden'
	reject PARSE_ERR do run --allow-run --lang kash -c '(nop "printf") forbidden'
	check computed do run --allow-run --lang kash -c $'executable = (first ["printf"])\n$executable "%s" "computed"'
	test-step "$backend computed stage environments retain data and ordered overrides"
	check 'record value' do run --allow-run --lang kash -c $'exports = [MODE: "record value"]\n:env $exports printenv MODE'
	check 'record' do run --allow-run --lang kash -c ':MODE pair :env (nop [MODE: "record"]) printenv MODE'
	check 'pair' do run --allow-run --lang kash -c ':env (nop [MODE: "record"]) :MODE pair printenv MODE'
	check '@(missing) {(missing)} $missing' do run --allow-run --lang kash -c 'exports = [MODE: """@(missing) {(missing)} $missing"""]; :env $exports printenv MODE'
	check $'line\nbreak' do run --allow-run --lang kash -c ':env (nop [MODE: "line\nbreak"]) printenv MODE'
	check '' do run --allow-run --lang kash -c ':env (nop [MODE: ""]) printenv MODE'
	check $'left\nright' do run --allow-run --lang kash -c ':env (nop [MODE: "left"]) printenv MODE | :env (nop [MODE: "right"]) sh -c "cat; printenv MODE"'
	check 'parent' do run --allow-run --env MODE=parent --lang kash -c $':env (parse-json "{}") true\nprintenv MODE'
	check 'lowered' do run --allow-run --lang kash -c '(run! :env [MODE: "lowered"] "printenv" "MODE")'
	check safe do run --allow-run --lang kash -c $'if (nop :false)\n\t:env (nop [TOKEN: (env "K07_SECRET")]) printf forbidden\nelse\n\tprintf safe'
	check '' do run --dry-run --allow-run --lang kmk -c 'dry :' dry --lang kash -c ':env (nop [TOKEN: (env "K07_SECRET")]) printf forbidden'
	check '' do run --dry-run --allow-run --lang kmk -c 'dry :' dry --lang kash -c '(run! :env [TOKEN: (env "K07_SECRET")] "true")'
	for value in ':nil' '"text"' '["list"]' '[MODE: 1]' '[MODE: :nil]' '[MODE: ["nested"]]' '(parse-json "{\"bad=name\":\"secret\"}")' '(parse-json "{\"1BAD\":\"secret\"}")'; do
		reject EXPR_INVALID do run --allow-run --lang kash -c ":env (nop $value) printf forbidden"
	done
	printf '\0' >"$work/nul"
	reject EXPR_INVALID do run --allow-run --allow-read --lang kash -c ':env (nop [MODE: (text (read ./nul))]) printf forbidden'
	reject PARSE_ERR do run --allow-run --lang kash -c ':env (nop []) :env (nop []) true'
	reject CAP_DENIED do run --allow-read --lang kash -c ':env (nop [MODE: "denied"]) printf forbidden'
	test-step "$backend secrets are fetched only at execution and omitted from traces"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
exports = [TOKEN: (env "K07_SECRET")]
chosen :
	:env $exports sh -c "printf %s \"\$TOKEN\" > received"
bad :
	:env (nop [TOKEN: [(env "K07_SECRET")]]) true
KMK
	if "${runner[@]}" do plan -C "$work" chosen >"$work/plan" 2>"$work/err"; then test-ok "$backend planning defers computed env"; else test-fail "$backend planning evaluated runtime env"; cat "$work/err"; fi
	check '' --dry-run chosen
	if [ ! -e "$work/received" ]; then test-ok "$backend inspection has no effects"; else test-fail "$backend inspection launched secret consumer"; fi
	"${runner[@]}" do run -C "$work" --json --allow-run --allow-env=K07_SECRET "$work/Makefile.kmk" chosen >"$work/trace" 2>"$work/err"
	if [ "$(cat "$work/received")" = "$K07_SECRET" ] && ! grep -q "$K07_SECRET" "$work/trace" "$work/err"; then test-ok "$backend computed secrets reach only the child"; else test-fail "$backend environment trace leaked data"; fi
	reject EXPR_INVALID do run --allow-run --allow-env=K07_SECRET "$work/Makefile.kmk" bad
	if ! grep -q "$K07_SECRET" "$work/out" "$work/err"; then test-ok "$backend validation omits evaluated env data"; else test-fail "$backend validation leaked data"; fi
	test-step "$backend env suspension retains effects and tracked values invalidate cache"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
chosen :
	exports = (let [] (out! "once") [TOKEN: (text (read ./late))])
	:env $exports printenv TOKEN
./late :
	printf payload > @>
KMK
	check $'payload\nonce' chosen
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
exports = [TOKEN: (env "K07_SECRET")]
task chosen :
	:env $exports sh -c "printf x >> runs; printf %s \"\$TOKEN\" > received"
KMK
	check '' do run --allow-run --allow-env=K07_SECRET "$work/Makefile.kmk" chosen
	check '' do run --allow-run --allow-env=K07_SECRET "$work/Makefile.kmk" chosen
	if [ "$(cat "$work/runs")" = x ]; then test-ok "$backend computed env permits warm reuse"; else test-fail "$backend computed env reran"; fi
	K07_SECRET=changed check '' do run --allow-run --allow-env=K07_SECRET "$work/Makefile.kmk" chosen
	if [ "$(cat "$work/runs")" = xx ] && [ "$(cat "$work/received")" = changed ]; then test-ok "$backend changed env invalidates task"; else test-fail "$backend stale computed env"; fi
	test-step "$backend named operands, function arguments and invalid context"
	check '<one><two words>' do run --allow-run --lang kash -c 'printf "<%s>" $args' -- one 'two words'
	check 'one-two' do run --allow-run --lang kash -c $'(f one two) = (join "-" args)\nprintf "%s" (f "one" "two")'
	check '<one><two words>' do run --allow-run --lang kash -c $'script = (kash """printf "<%s>" $args""")\n(script "one" "two words")'
	check '"one"' do run --lang expr -c 'args.0' -- one
	check '"shadow"' do run --lang expr -c '(let [inputs "shadow"] inputs)'
	for expression in '(let [module/value 42] module/value)' '(def module/value 42)' '(def module/function [value] value)' '(def invalid [valid module/value] module/value)'; do
		reject DEF_INVALID do run --lang expr -c "$expression"
	done
	for name in inputs outputs inputs/changed; do reject SEL_NO_CONTEXT do run --lang kash -c "(nop $name)"; done
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
(selected-files UNUSED) = inputs/changed
selected = (selected-files :nil)
./output : ./input ./other | prepare
	(if (eq inputs @<*) :nil (fail! "inputs differ"))
	(if (eq outputs @>*) :nil (fail! "outputs differ"))
	(if (eq inputs/changed @<?) :nil (fail! "changed inputs differ"))
	(if (eq selected inputs/changed) :nil (fail! "lazy frame differs"))
	printf "<%s>" (map basename inputs) > (first outputs)
	printf "<%s>" (map basename inputs/changed) > ./changed
prepare :
	(nop args)
empty :
	(if (eq inputs []) :nil (fail! "task inputs not empty"))
	(if (eq outputs ["empty"]) :nil (fail! "task output name lost"))
invalid :
	(nop inputs/changed)
KMK
	printf first >"$work/input"
	printf other >"$work/other"
	check '' ./output
	if [ "$(cat "$work/output")" = '<input><other>' ] && [ "$(cat "$work/changed")" = '<input><other>' ]; then test-ok "$backend named frame retains normal input order and outputs"; else test-fail "$backend named rule context"; fi
	check '' empty
	reject SEL_NO_CONTEXT invalid
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
(changed-files UNUSED) = inputs/changed
./named-output : ./input ./other | prepare
	printf "%s" (join "," (map basename (changed-files :nil))) > ./selected
	printf "%s" (join "," (map basename inputs)) > (first outputs)
prepare :
	(nop args)
KMK
	check '' ./named-output
	if [ "$(cat "$work/selected")" = input,other ]; then test-ok "$backend named-only selector obtains first-run snapshot"; else test-fail "$backend named-only snapshot"; fi
	printf changed >"$work/input"
	touch -m -d '+2 seconds' "$work/input"
	check '' ./named-output
	if [ "$(cat "$work/selected")" = input ]; then test-ok "$backend named changed inputs exclude unchanged and order-only inputs"; else test-fail "$backend named changed-input selection"; fi
	cat >"$work/Makefile.kmk" <<'KMK'
./shell-output : ./input
	printf "%s" @(join "," (map basename inputs)) > @>
	printf "%s" @(count args) > ./shell-args
KMK
	check '' ./shell-output
	if [ "$(cat "$work/shell-output")" = input ] && [ "$(cat "$work/shell-args")" = 0 ]; then test-ok "$backend external-shell templates share named context and empty invocation args"; else test-fail "$backend external-shell named context"; fi
	test-step "$backend formatting preserves namespaces and typed setup"
	printf '%s\n' ':env (nop [MODE: "data"]) printf "%s" $inputs/changed (first outputs) $args' >"$TMPDIR/source.kash"
	"${runner[@]}" do parse --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.ast"
	"${runner[@]}" do fmt --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.fmt"
	"${runner[@]}" do fmt --lang kash "$TMPDIR/$backend.fmt" >"$work/again"
	if cmp -s "$TMPDIR/$backend.fmt" "$work/again"; then test-ok "$backend formatting is idempotent"; else test-fail "$backend formatting"; fi
done
test-step "native/WASM AST and format parity"
for kind in ast fmt; do if cmp -s "$TMPDIR/native.$kind" "$TMPDIR/wasm.$kind"; then test-ok "$kind parity"; else test-fail "$kind parity"; fi; done
test-end
