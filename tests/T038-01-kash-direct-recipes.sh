#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — direct authored Kash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-01 direct Kash recipes and expressions"
test-step "build native and WASM artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
printf '%s\n' '(nop [status: 0])' 'if (= 1 1)' '  printf "%s" (join "-" ["a" "b"])' 'printf "%s" ./@(join " " ["two" "words"])/file' >"$TMPDIR/source.kash"
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
	check '["+files" "-symlinks" "+true" "-nil"]' do run --lang expr -c '[:+files :-symlinks :+true :-nil]'
	check '["+files" "-dir"]' do run --print-result --lang km -c $'flags = [:+files :-dir]\nflags'
	check '+files|-symlinks' do run --allow-run --lang kash -c 'printf "%s|%s" (nop :+files) (nop :-symlinks)'
	for symbol in ':+ ' ':-1' ':++files' ':--files'; do
		reject PARSE_ERR do run --lang expr -c "$symbol"
	done
	test-step "$backend structural path interpolation preserves data and glob provenance"
	check '"./two words/file"' do run --lang expr -c '@(nop "./two words")/file'
	check '"./build/a b/main.js"' do run --lang expr -c './build/@(join " " ["a" "b"])/main.js'
	check './two words/file' do run --allow-run --lang kash -c 'printf "%s" @(nop "./two words")/file'
	check './two words/file' do run --allow-run --lang kash -c 'printf "%s" ./@(join " " ["two" "words"])/file'
	data=$'$(touch forbidden) @(missing) {(missing)} "quotes"\nline'
	printf 'name = """%s"""\nresult = ./@(name)/file\n' "$data" >"$work/path-data.km"
	check "$(jq -cn --arg name "./$data/file" '$name')" do run path-data.km result
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend interpolated path bytes are never source"; else test-fail "$backend interpolated path executed data"; fi
	for value in ':nil' ':true' '42' '[]' '[a: 1]' '([x] x)'; do
		reject EXPR_INVALID do run --lang expr -c "./@($value)/file"
	done
	reject EXPR_INVALID do run --lang expr -c '@(nop "src")/file'
	mkdir -p "$work/literal*?[x]\\name" "$work/literalZZxname"
	printf literal >"$work/literal*?[x]\\name/a.txt"
	printf decoy >"$work/literalZZxname/a.txt"
	cat >"$work/path-glob.km" <<'KM'
root = "./literal*?[x]\\name"
matches = (wildcard @(root)/*.txt)
KM
	check '["./literal*?[x]\\name/a.txt"]' do run --allow-read="$work/literal*?[x]\\name" path-glob.km matches
	reject CAP_DENIED do run --allow-read="$work/literalZZxname" path-glob.km matches
	check '["./literal*?[x]\\name/a.txt"]' do run --allow-read --lang expr -c '(let [root "./literal*?[x]\\name"] (wildcard (nop @(root)/*.txt)))'
	printf '%s\n' './@(join " " ["a" "b"])/file' | "${runner[@]}" do parse --lang expr --json >"$work/path-ast.json"
	if jq -e '.ast.kind == "path" and (.ast.parts | length) == 3 and .ast.parts[1].expression.kind == "application"' "$work/path-ast.json" >/dev/null; then test-ok "$backend path AST exposes interpolation children"; else test-fail "$backend path AST lost children"; cat "$work/path-ast.json"; fi
	test-step "$backend expression statements and typed arguments"
	check '' do run --lang kash -c '(nop [status: 0])'
	check '' do run --lang kash -c $'if (nop :true)\n\t(nop [status: 0])'
	check 'a-bvalue' do run --allow-run --lang kash -c $'(nop :nil)\nprintf "%s" (join\n "-" ["a" "b"])\nif (=\n 1 1)\n\tprintf "%s" (nop "value")\nelse\n\t(fail! "unselected")'
	check 'captured' do run --allow-run --lang kash -c $'name = $(printf captured)\nmatch $name\n\tcase "captured"\n\t\tprintf "%s" $name'
	check 'expression' do run --allow-run --lang kash -c $':MODE (nop "expression") printenv MODE'
	test-step "$backend direct recipes preserve data, selectors and rule arguments"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = (nop kash)
data = """@if(:false) $(touch forbidden) {(missing)} "quotes" end"""
chosen {mode=sso} :
	if (= mode "sso")
		printf "%s" $data
	else
		(fail! "unselected")
./nested/output :
	printf "%s" "artifact" > @>
generated-source = """printf "%s" (join "-" ["generated" "direct"])"""
generate generated = [[kind: "task" target: "generated" inputs: [] order-only: [] recipe: [generated-source]]]
KMK
	check '@if(:false) $(touch forbidden) {(missing)} "quotes" end' chosen
	check '' ./nested/output
	if [ "$(cat "$work/nested/output")" = artifact ] && [ ! -e "$work/forbidden" ]; then test-ok "$backend data is never reparsed and selectors retain the frame"; else test-fail "$backend direct data or publication"; fi
	check 'generated-direct' generated
	test-step "$backend typed argv needs no source serialization or escaping"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
data = ["" "two words" "line\nbreak" "\"quotes\"" """$missing @(missing) {(missing)}""" """\@(missing)"""]
script = (kash """printf "<%s>" $data""")
chosen :
	printf "<%s>" $data
	printf "<%s>" (nop data)
	printf "<%s>" "@(missing) {(missing)}"
captured :
	printf "<%s>" $(printf "%s" "@(missing) {(missing)}")
callable :
	(script)
explicit :
	generated = (kash (render [name: "explicit"] "plain" """printf "%s" "@(name)" """))
	(generated)
KMK
	argv_expected=$'<><two words><line\nbreak><"quotes"><$missing @(missing) {(missing)}><\\@(missing)>'
	check "${argv_expected}${argv_expected}<@(missing) {(missing)}>" chosen
	check '<@(missing) {(missing)}>' captured
	check "$argv_expected" callable
	check explicit explicit
	test-step "$backend selected syntax is checked before any statement executes"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
chosen :
	printf forbidden
	if (nop :false)
		printf "%s" (nop
KMK
	reject PARSE_ERR chosen
	reject PARSE_ERR --dry-run chosen
	test-step "$backend migration diagnostics and external-shell templates"
	for source in 'printf @(name)' '\@(out! "forbidden")' 'name = @(out! "forbidden")' ':cwd @(name) printf forbidden' 'printf forbidden > @(name)' '@if(:true)'; do
		printf 'SHELL = kash\nchosen :\n\t%s\n' "$source" >"$work/Makefile.kmk"
		reject PARSE_ERR chosen
		reject PARSE_ERR do run --allow-run --lang kash -c "$source"
	done
	cat >"$work/Makefile.kmk" <<'KMK'
name = "external"
chosen :
	printf "%s" "@(name)"
KMK
	check external chosen
	prefix=$'SHELL = kash\nchosen :\n\t'
	head='kame-038-missing-head'
	printf '%s%s\n' "$prefix" "$head" >"$work/Makefile.kmk"
	status=0
	"${runner[@]}" -C "$work" --json chosen >"$work/diagnostic.jsonl" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && jq -e -s --arg source "$work/Makefile.kmk" --argjson start "${#prefix}" --argjson end "$((${#prefix}+${#head}))" 'any(.[]; .diagnostic.code == "TOOL_MISSING" and .diagnostic.source == $source and .diagnostic.span.start == $start and .diagnostic.span.end == $end)' "$work/diagnostic.jsonl" >/dev/null; then test-ok "$backend missing tool retains exact authored head span"; else test-fail "$backend authored head diagnostic"; cat "$work/diagnostic.jsonl"; fi
	test-step "$backend suspension retains effects and publication remains atomic"
	printf 'input' >"$work/input"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
./published : ./input
	value = (let [] (out! "once") "A")
	(write! ./side-output "staged")
	(yield! (str value (text (read ./late))))
./late : ./input ; [shell: "/bin/sh"]
	printf B > @>
./failed : ./input
	(write! ./failed-side "must-not-publish")
	(fail! "intentional")
KMK
	check once ./published
	if [ "$(cat "$work/published")" = AB ] && [ "$(cat "$work/side-output")" = staged ]; then test-ok "$backend suspended effects publish once"; else test-fail "$backend suspension or publication"; fi
	reject USER_ERROR ./failed
	if [ ! -e "$work/failed-side" ] && [ ! -e "$work/failed" ]; then test-ok "$backend failed recipe leaves no staged output"; else test-fail "$backend failure published writes"; fi
	if timeout 15 "${runner[@]}" do plan -C "$work" --json ./failed >"$work/plan" 2>"$work/err" && [ ! -e "$work/failed-side" ]; then test-ok "$backend inspection does not execute the recipe"; else test-fail "$backend inspection boundary"; cat "$work/err"; fi
	test-step "$backend managed service keeps direct Kash ownership"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
service daemon : ; [ready: [argv: ["test" "-f" "service-ready"] interval-ms: 10 timeout-ms: 3000]]
	sh -c (nop """touch service-ready; printf %s "$$" > service-pid; exec sleep 30""")
consumer : daemon
	printf service
KMK
	check service consumer
	if [ -s "$work/service-pid" ]; then
		pid="$(cat "$work/service-pid")"
		for attempt in {1..100}; do if ! kill -0 "$pid" 2>/dev/null; then break; fi; sleep .01; done
		if ! kill -0 "$pid" 2>/dev/null; then test-ok "$backend service released its process"; else test-fail "$backend service process survived"; kill "$pid" 2>/dev/null || true; fi
	else test-fail "$backend service did not start"; fi
	test-step "$backend every reached command head is a tracked tool"
	mkdir -p "$work/startup tools" "$work/child tools"
	printf '#!/bin/sh\nprintf startup\n' >"$work/startup tools/kame-direct-tool"
	printf '#!/bin/sh\nprintf child\n' >"$work/child tools/kame-direct-tool"
	printf '#!/bin/sh\nprintf override\n' >"$work/override-tool"
	chmod +x "$work/startup tools/kame-direct-tool" "$work/child tools/kame-direct-tool" "$work/override-tool"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
chosen :
	command = "kame-direct-tool"
	resolved = x/kame-direct-tool
	kame-direct-tool
	$command
	$x/kame-direct-tool
	${x/kame-direct-tool}
	$resolved
	printf "%s" $(kame-direct-tool | cat)
	:PATH "./child tools" kame-direct-tool
	if (nop :false)
		kame-unused-missing-tool
KMK
	PATH="$work/startup tools:$PATH" check 'startupstartupstartupstartupstartupstartupstartup' chosen
	PATH="$work/startup tools:$PATH" check 'overrideoverrideoverrideoverrideoverrideoverrideoverride' --tool kame-direct-tool=./override-tool chosen
	reject TOOL_MISSING chosen
	printf 'SHELL = kash\nchosen :\n\t./override-tool\n' >"$work/Makefile.kmk"
	check override do run --allow-run="$work/override-tool" "$work/Makefile.kmk" chosen
	printf 'SHELL = kash\nchosen :\n\tprintf denied\n' >"$work/Makefile.kmk"
	reject CAP_DENIED do run --allow-run="$work/other-tool" --tool printf=./override-tool "$work/Makefile.kmk" chosen
	test-step "$backend executable identity invalidates persisted freshness"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
./cached : ./input
	./cached-tool @>
KMK
	printf '#!/bin/sh\nprintf first > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	chmod +x "$work/cached-tool"
	check '' ./cached
	check '' ./cached
	if [ "$(cat "$work/tool-runs")" = x ]; then test-ok "$backend unchanged executable permits reuse"; else test-fail "$backend executable reuse"; fi
	printf '#!/bin/sh\nprintf second > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	check '' ./cached
	if [ "$(cat "$work/tool-runs")" = xx ] && [ "$(cat "$work/cached")" = second ]; then test-ok "$backend changed executable invalidates reuse"; else test-fail "$backend executable freshness"; fi
	test-step "$backend retained watch observes executable edits"
	timeout 20 "${runner[@]}" -C "$work" --json --watch ./cached >"$work/watch.log" 2>"$work/watch.err" &
	watcher=$!
	for attempt in {1..200}; do if grep -q '"type":"watch-idle"' "$work/watch.log"; then break; fi; sleep .025; done
	printf '#!/bin/sh\nprintf watched > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	for attempt in {1..300}; do if [ "$(cat "$work/cached")" = watched ] && [ "$(grep -c '"type":"watch-idle"' "$work/watch.log")" -ge 2 ]; then break; fi; sleep .025; done
	if [ "$(cat "$work/cached")" = watched ]; then test-ok "$backend watch retains executable dependency"; else test-fail "$backend executable watch"; cat "$work/watch.err"; fi
	kill -TERM "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
	test-step "$backend parenthesized formatting and AST parity"
	"${runner[@]}" do parse --lang kash "$TMPDIR/source.kash" >"$TMPDIR/$backend.ast"
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
