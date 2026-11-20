#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — direct authored Kash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-01 direct Kash recipes and expressions"
test-step "build native and WASM artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
printf '%s\n' '(nop {status: 0})' 'if (1 == 1)' '  printf "%s" (join "-" ["a", "b"])' 'printf "%s" (nop ./${join " " ["two", "words"]}/file)' >"$TMPDIR/source.kash"
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	check() {
		local expected="$1"; shift
		local invocation=("${runner[@]}")
		if [ "$1" = do ]; then invocation+=("$1" "$2"); shift 2; fi
		if timeout 15 "${invocation[@]}" -C "$work" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$expected" ]; then
			test-ok "$backend direct evaluation"
		else
			test-fail "$backend direct evaluation"
			cat "$work/out" "$work/err"
		fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		local invocation=("${runner[@]}")
		if [ "$1" = do ]; then invocation+=("$1" "$2"); shift 2; fi
		timeout 15 "${invocation[@]}" -C "$work" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code before effects"; else test-fail "$backend rejection ($status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend signed symbols are ordinary text values"
	check '[:+files, :-symlinks, :+true, :-nil]' do run --lang expr -c '[:+files, :-symlinks, :+true, :-nil]'
	check '[:+files, :-dir]' do run --print-result --lang km -c $'flags = [:+files, :-dir]\nflags'
	check '+files|-symlinks' do run --allow-run --lang kash -c 'printf "%s|%s" (nop :+files) (nop :-symlinks)'
	for symbol in ':+ ' ':-1' ':++files' ':--files'; do
		reject PARSE_ERR do run --lang expr -c "$symbol"
	done
	test-step "$backend structural path interpolation preserves data and glob provenance"
	check '"./two words/file"' do run --lang expr -c 'str (${nop "./two words"}/file)'
	check '"./build/a b/main.js"' do run --lang expr -c 'str (./build/${join " " ["a", "b"]}/main.js)'
	check './two words/file' do run --allow-run --lang kash -c 'printf "%s" (nop ${nop "./two words"}/file)'
	check './two words/file' do run --allow-run --lang kash -c 'printf "%s" (nop ./${join " " ["two", "words"]}/file)'
	data=$'$(touch forbidden) @(missing) {(missing)} "quotes"\nline'
	printf 'name = """%s"""\nresult = str (./${name}/file)\n' "$data" >"$work/path-data.km"
	check "./$data/file" do cat -f path-data.km result
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend interpolated path bytes are never source"; else test-fail "$backend interpolated path executed data"; fi
	for value in 'nil' 'true' '42' '[]' '{a: 1}' '(x => x)'; do
		reject EXPR_INVALID do run --lang expr -c "./\${nop $value}/file"
	done
	reject EXPR_INVALID do run --lang expr -c '${nop "src"}/file'
	mkdir -p "$work/literal*?[x]\\name" "$work/literalZZxname"
	printf literal >"$work/literal*?[x]\\name/a.txt"
	printf decoy >"$work/literalZZxname/a.txt"
	cat >"$work/path-glob.km" <<'KM'
root = "./literal*?[x]\\name"
matches = map str (wildcard ${root}/*.txt)
KM
	check '["./literal*?[x]\\name/a.txt"]' do run --allow-read="$work/literal*?[x]\\name" path-glob.km matches
	reject CAP_DENIED do run --allow-read="$work/literalZZxname" path-glob.km matches
	reject CAP_DENIED do run --allow-read="$work/literalZZxname" --lang expr -c '(wildcard ./literal*/a.txt)'
	reject CAP_DENIED do run --allow-read="$work/literalZZxname" --lang expr -c '(wildcard ./literalZZxname/../literal*/a.txt)'
	check '["./literal*?[x]\\name/a.txt"]' do run --allow-read --lang expr -c '(root => map str (wildcard (nop ${root}/*.txt))) "./literal*?[x]\\name"'
	printf '%s\n' './${join " " ["a", "b"]}/file' | "${runner[@]}" do parse --lang expr --json >"$work/path-ast.json"
	if jq -e '.ast.kind == "path" and (.ast.parts | length) == 3 and .ast.parts[1].expression.kind == "application"' "$work/path-ast.json" >/dev/null; then test-ok "$backend path AST exposes interpolation children"; else test-fail "$backend path AST lost children"; cat "$work/path-ast.json"; fi
	test-step "$backend expression statements and typed arguments"
	check '' do run --lang kash -c '(nop {status: 0})'
	check '' do run --lang kash -c $'if true\n\t(nop {status: 0})'
	check 'a-bvalue' do run --allow-run --lang kash -c $'(nop nil)\nprintf "%s" (join\n "-" ["a", "b"])\nif (1 ==\n 1)\n\tprintf "%s" (nop "value")\nelse\n\t(fail! "unselected")'
	check 'captured' do run --allow-run --lang kash -c $'let name = $(printf captured)\nmatch name\n\t"captured" =>\n\t\tprintf "%s" $name'
	check 'expression' do run --allow-run --lang kash -c $'MODE: (nop "expression") printenv MODE'
	test-step "$backend direct recipes preserve data, selectors and rule arguments"
	cat >"$work/Makefile.km" <<'KM'
SHELL = (nop kash)
data = """@if(:false) $(touch forbidden) {(missing)} "quotes" end"""
task chosen {mode=sso}
	if mode == "sso"
		printf "%s" $data
	else
		(fail! "unselected")
rule ./nested/output
	printf "%s" "artifact" > $outputs.0
def make-generated () =
	task generated
		printf "%s" (join "-" ["generated", "direct"])
generate make-generated ()
KM
	check '@if(:false) $(touch forbidden) {(missing)} "quotes" end' chosen
	check '' ./nested/output
	if [ "$(cat "$work/nested/output")" = artifact ] && [ ! -e "$work/forbidden" ]; then test-ok "$backend data is never reparsed and selectors retain the frame"; else test-fail "$backend direct data or publication"; fi
	check 'generated-direct' generated
	test-step "$backend typed argv needs no source serialization or escaping"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
data = ["", "two words", "line\nbreak", "\"quotes\"", """$missing @(missing) {(missing)}""", """\@(missing)"""]
def script ()
	printf "<%s>" $...data
task chosen
	printf "<%s>" $...data
	let expanded = nop data
	printf "<%s>" $...expanded
	printf "<%s>" "@(missing) {(missing)}"
task captured
	printf "<%s>" $(printf "%s" "@(missing) {(missing)}")
task callable
	(script ())
task explicit
	printf "%s" (render {name: "explicit"} "plain" """@{name}""")
KM
	argv_expected=$'<><two words><line\nbreak><"quotes"><$missing @(missing) {(missing)}><\\@(missing)>'
	check "${argv_expected}${argv_expected}<@(missing) {(missing)}>" chosen
	check '<@(missing) {(missing)}>' captured
	check "$argv_expected" callable
	check explicit explicit
	test-step "$backend explicit argv spreads include command heads"
	check '<two words><><tail>' do run --allow-run --lang kash -c $'def CMD = ["printf", "<%s>", "two words", ""]\n$...CMD tail'
	check '<1><true><two words>' do run --allow-run --lang kash -c $'def args = [1, true, "two words"]\nprintf "<%s>" $...args'
	check '<tail>' do run --allow-run --lang kash -c $'def args = []\nprintf "<%s>" $...args tail'
	check '<field>' do run --allow-run --lang kash -c $'def commands = {main: ["printf", "<%s>"]}\n$...{commands.main} field'
	check '<pipeline>' do run --allow-run --lang kash -c $'def CMD = ["printf", "<%s>"]\ncwd: . $...CMD pipeline | cat'
	check '<capture>' do run --allow-run --lang kash -c $'def CMD = ["printf", "<%s>"]\nprintf "%s" $($...CMD capture)'
	check 'selected' do run --allow-run --lang kash -c $'def CMD = ["true"]\nif $?($...CMD).ok\n  printf selected'
	check 'recovered' do run --allow-run --lang kash -c $'def CMD = ["false"]\n$...CMD ? printf recovered'
	check '' do run --allow-run --lang kash -c $'def CMD = ["true"]\n$...CMD &'
	check '<$missing @(missing) ; | *>' do run --allow-run --lang kash -c $'def CMD = ["printf", "<%s>", """$missing @(missing) ; | *"""]\n$...CMD'
	for value in '[]' '[""]' '[1]' '[true]' '["printf", []]' '["printf", nil]' '["printf", {key: "value"}]' '"printf"' 'nil'; do
		reject EXPR_INVALID do run --allow-run --lang kash -c "def CMD = $value"$'\n$...CMD printf forbidden'
	done
	reject EXPR_INVALID do run --allow-run --lang kash -c $'def CMD = ["printf"]\n$CMD forbidden'
	reject EXPR_INVALID do run --allow-run --lang kash -c $'def args = "scalar"\nprintf "%s" $...args'
	printf '\0' >"$work/nul"
	reject EXPR_INVALID do run --allow-run --allow-read --lang kash -c $'def CMD = ["printf", (text (read ./nul))]\n$...CMD'
	reject EXPR_INVALID do run --allow-run --allow-read --lang kash -c $'def CMD = [(text (read ./nul))]\n$...CMD'
	for source in 'printf "$...CMD"' 'printf prefix$...CMD' 'printf $...{CMD}suffix' 'cwd: $...CMD printf bad' 'printf > $...CMD' 'let value = $...CMD'; do
		reject PARSE_ERR do run --allow-run --lang kash -c $'printf forbidden\n'"$source"
	done
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
CMD = ["printf", "<%s>", "recipe argument"]
task chosen
	$...CMD tail
KM
	check '<recipe argument><tail>' chosen
	test-step "$backend selected syntax is checked before any statement executes"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
task chosen
	printf forbidden
	if false
		printf "%s" (nop
KM
	reject PARSE_ERR chosen
	reject PARSE_ERR --dry-run chosen
	test-step "$backend Kash word boundaries and external-shell templates"
	for source in 'printf @{name}' 'printf prefix@{out! "forbidden"}' 'let name = @{out! "forbidden"}' 'cwd: @{name} printf forbidden' 'printf forbidden > @{name}' '@{true}'; do
		printf 'task chosen\n\t%s\n' "$source" >"$work/Makefile.km"
		reject PARSE_ERR chosen
		reject PARSE_ERR do run --allow-run --lang kash -c "$source"
	done
	cat >"$work/Makefile.km" <<'KM'
SHELL = "sh"
name = "external"
task chosen
	printf "%s" "@{name}"
KM
	check external chosen
	prefix=$'task chosen\n\t'
	head='kame-038-missing-head'
	printf '%s%s\n' "$prefix" "$head" >"$work/Makefile.km"
	status=0
	"${runner[@]}" -C "$work" --json chosen >"$work/diagnostic.jsonl" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && jq -e -s --arg source "$work/Makefile.km" --argjson start "${#prefix}" --argjson end "$((${#prefix}+${#head}))" 'any(.[]; .diagnostic.code == "TOOL_MISSING" and .diagnostic.source == $source and .diagnostic.span.start == $start and .diagnostic.span.end == $end)' "$work/diagnostic.jsonl" >/dev/null; then test-ok "$backend missing tool retains exact authored head span"; else test-fail "$backend authored head diagnostic"; cat "$work/diagnostic.jsonl"; fi
	test-step "$backend suspension retains effects and publication remains atomic"
	printf 'input' >"$work/input"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
rule ./published : ./input
	(out! "once")
	let value = "A"
	(write! ./side-output "staged")
	return (str value (text (read ./late)))
rule ./late : ./input ; {shell: "/bin/sh"}
	printf B > @{outputs.0}
rule ./failed : ./input
	(write! ./failed-side "must-not-publish")
	(fail! "intentional")
KM
	check once ./published
	if [ "$(cat "$work/published")" = AB ] && [ "$(cat "$work/side-output")" = staged ]; then test-ok "$backend suspended effects publish once"; else test-fail "$backend suspension or publication"; fi
	reject USER_ERROR ./failed
	if [ ! -e "$work/failed-side" ] && [ ! -e "$work/failed" ]; then test-ok "$backend failed recipe leaves no staged output"; else test-fail "$backend failure published writes"; fi
	if timeout 15 "${runner[@]}" do plan -C "$work" --json ./failed >"$work/plan" 2>"$work/err" && [ ! -e "$work/failed-side" ]; then test-ok "$backend inspection does not execute the recipe"; else test-fail "$backend inspection boundary"; cat "$work/err"; fi
	test-step "$backend every reached command head is a tracked tool"
	mkdir -p "$work/startup tools" "$work/child tools"
	printf '#!/bin/sh\nprintf startup\n' >"$work/startup tools/kame-direct-tool"
	printf '#!/bin/sh\nprintf child\n' >"$work/child tools/kame-direct-tool"
	printf '#!/bin/sh\nprintf override\n' >"$work/override-tool"
	chmod +x "$work/startup tools/kame-direct-tool" "$work/child tools/kame-direct-tool" "$work/override-tool"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
task chosen
	let command = "kame-direct-tool"
	let resolved = x/kame-direct-tool
	kame-direct-tool
	$command
	x/kame-direct-tool
	${x/kame-direct-tool}
	$resolved
	printf "%s" $(kame-direct-tool | cat)
	PATH: "./child tools" kame-direct-tool
	if false
		kame-unused-missing-tool
KM
	PATH="$work/startup tools:$PATH" check 'startupstartupstartupstartupstartupstartupstartup' chosen
	PATH="$work/startup tools:$PATH" check 'overrideoverrideoverrideoverrideoverrideoverrideoverride' --tool kame-direct-tool=./override-tool chosen
	check override do run --allow-run --tool kame-direct-tool=./override-tool --lang kash -c $'def CMD = ["kame-direct-tool"]\n$...CMD'
	check override do run --allow-run --tool kame-direct-tool=./override-tool --lang kash -c $'def CMD = [x/kame-direct-tool]\n$...CMD'
	reject TOOL_MISSING do run --allow-run --lang kash -c $'def CMD = ["kame-038-missing-spread-tool"]\n$...CMD'
	reject TOOL_MISSING chosen
	printf 'task chosen\n\t./override-tool\n' >"$work/Makefile.km"
	check override do run --allow-run="$work/override-tool" "$work/Makefile.km" chosen
	printf 'task chosen\n\tprintf denied\n' >"$work/Makefile.km"
	reject CAP_DENIED do run --allow-run="$work/other-tool" --tool printf=./override-tool "$work/Makefile.km" chosen
	test-step "$backend executable identity invalidates persisted freshness"
	cat >"$work/Makefile.km" <<'KM'
SHELL = kash
rule ./cached : ./input
	./cached-tool $outputs.0
KM
	printf '#!/bin/sh\nprintf first > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	chmod +x "$work/cached-tool"
	check '' ./cached
	check '' ./cached
	if [ "$(cat "$work/tool-runs")" = x ]; then test-ok "$backend unchanged executable permits reuse"; else test-fail "$backend executable reuse"; fi
	printf '#!/bin/sh\nprintf second > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	check '' ./cached
	if [ "$(cat "$work/tool-runs")" = xx ] && [ "$(cat "$work/cached")" = second ]; then test-ok "$backend changed executable invalidates reuse"; else test-fail "$backend executable freshness"; fi
	test-step "$backend parenthesized formatting and AST parity"
	printf '%s\n' '$...CMD $...{project.args}' >"$work/spread.kash"
	"${runner[@]}" do fmt --lang kash "$work/spread.kash" >"$work/spread.fmt"
	if cmp -s "$work/spread.kash" "$work/spread.fmt"; then test-ok "$backend formatting preserves explicit spreads"; else test-fail "$backend spread formatting"; cat "$work/spread.fmt"; fi
	"${runner[@]}" do parse --json --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.ast"
	"${runner[@]}" do fmt --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.fmt"
	"${runner[@]}" do fmt --lang kash "$TMPDIR/$backend.fmt" >"$work/again"
	if cmp -s "$TMPDIR/$backend.fmt" "$work/again"; then test-ok "$backend formatting is idempotent"; else test-fail "$backend formatting"; fi
done
test-step "native/WASM parse and format parity"
for kind in ast fmt; do
	if cmp -s "$TMPDIR/native.$kind" "$TMPDIR/wasm.$kind"; then test-ok "$kind parity"; else test-fail "$kind parity"; fi
done
if cmp -s "$TMPDIR/native/path-ast.json" "$TMPDIR/wasm/path-ast.json"; then test-ok "interpolated path AST parity"; else test-fail "interpolated path AST parity"; fi
test-end
