#!/usr/bin/env bash
# Spec: docs/spec/016-templates.md — reference/call and directive boundaries
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T016-03 directive boundaries and canonical interpolation"
test-step "build both hosts"
cd "$CLI_ROOT"
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
cli_build
work="$TMPDIR/boundaries"
mkdir -p "$work"
printf exists >"$work/input"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	check() {
		local expected="$1"; shift
		if "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend boundary"; else test-fail "$backend boundary: $* (expected $expected)"; cat "$work/out" "$work/err"; fi
	}
	check_bytes() {
		local expected="$1"; shift
		printf '%s' "$expected" >"$work/expected"
		if "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && cmp -s "$work/out" "$work/expected"; then test-ok "$backend exact boundary bytes"; else test-fail "$backend exact boundary bytes: $*"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		"${runner[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend rejection ($status), expected $code: $*"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend directive operands, bindings and relative includes"
	check yes do render --allow-read -c "@if exists? $work/input"$'\nyes\n@end if'
	check yes do render --allow-read -c "@if (exists? $work/input)"$'\nyes\n@end if'
	check yes do render --define flag=yes -c $'@if flag\nyes\n@else\nno\n@end if'
	check '"yes\n"' do run --lang expr -c $'render {ready: (() => true)} """@if ready ()\nyes\n@end if"""'
	check right do render --comment hash -c $'# @if false\nwrong\n# @else\nright\n# @end if'
	check yes do render -c $'@if false\nno\n@elif "x" == "x"\nyes\n@end if'
	check yes do render -c $'@if (x => x == "yes") "yes"\nyes\n@end if'
	check Ada do render -c $'@with nop {name: "Ada"}\n@{name}\n@end with'
	check yes do render -c $'@match str "a" "b"\n@case "ab"\nyes\n@else\nno\n@end match'
	check first do render -c $'@match "value"\n@case "value"\nfirst\n@case "value"\n@{fail! "second case"}\n@else\n@{fail! "fallback case"}\n@end match'
	check '"first"' do run --lang expr -c $'match "value"\n\t"value" => "first"\n\t"value" => fail! "second case"\n\telse => fail! "fallback case"'
	check_bytes '' do render -c $'@match "value"\n@case "different"\n@{fail! "unmatched case"}\n@end match'
	multiline=$'  before @{match "value"\n    "value" => "first"\n  } after\n'
	check_bytes $'  before first after\n' do render -c "$multiline"
	check_bytes 'AyesBC' do render --comment html -c $'A@{match "a"\n\t"a" => "yes"\n}<!-- @if true -->B<!-- @end if -->C'
	"${runner[@]}" do fmt --lang template -c "$multiline" >"$work/multiline.fmt"
	check_bytes $'  before @{\nmatch "value"\n\t"value" => "first"} after\n' do fmt --lang template "$work/multiline.fmt"
	check_bytes $'  before first after\n' do render --comment plain "$work/multiline.fmt"
	check_bytes $'a\nb\n' do render -c $'@for item in list "a" "b"\n@{item}\n@end for'
	check_bytes $'a\n, b\n' do render -c $'@for item in list "a" "b" sep str "," " "\n@{item}\n@end for'
	check 'a/b' do render -c $'@for head rest... in [["a", "b"]]\n@{head}/@{first rest}\n@end for'
	check Ada do render -c $'@let name = str "A" "da"\n@{name}'
	printf '@{name}' >"$work/child.md"
	check Ada do render --allow-read -c "@include (str \"$work/\" \"child.md\") {name: \"Ada\"}"
	printf '<!-- @include ./child.md -->\n' >"$work/second.md"
	printf '<!-- @include ./second.md -->\n' >"$work/first.md"
	check Ada do render --allow-read -c "@include \"$work/first.md\" {name: \"Ada\"}"
	mkdir -p "$work/parts"
	printf '<!-- @include ./parts/../cycle.md -->\n' >"$work/cycle.md"
	reject TPL_CYCLE do render --allow-read -c "@include \"$work/cycle.md\""

	test-step "$backend template dependencies and foreign shell recipes"
	build="$work/build-$backend"
	mkdir -p "$build/docs"
	printf 'rule ./output = render ./docs/page.kmt\n' >"$build/Kamefile"
	printf '@include ./row.kmt\n' >"$build/docs/page.kmt"
	for content in first second; do
		printf '%s' "$content" >"$build/docs/row.kmt"
		if (cd "$build" && "${runner[@]}" --allow-read --allow-write ./output) >"$work/out" 2>"$work/err" &&
			[ "$(cat "$build/output")" = "$content" ]; then
			test-ok "$backend publishes $content from nested template dependency"
		else
			test-fail "$backend nested template dependency: $(cat "$work/err")"
		fi
	done
	printf '%s\n' 'rule ./tracked = render ./tracked.kmt' >"$build/Kamefile"
	printf '%s' '@{join "," (map basename (files ./watched/*.txt))}|@{text (read ./payload)}|@{env/KAME_TEMPLATE_TRACKED}' >"$build/tracked.kmt"
	mkdir "$build/watched"
	touch "$build/watched/a.txt"
	printf before >"$build/payload"
	for expected in 'a.txt|before|one' 'a.txt,b.txt|before|one' 'a.txt,b.txt|after|one' 'a.txt,b.txt|after|two'; do
		if KAME_TEMPLATE_TRACKED="${expected##*|}" "${runner[@]}" -C "$build" --allow-env=KAME_TEMPLATE_TRACKED --allow-read --allow-write ./tracked >"$work/out" 2>"$work/err" &&
			[ "$(cat "$build/tracked")" = "$expected" ]; then
			test-ok "$backend publishes tracked template value $expected"
		else
			test-fail "$backend tracked template expected $expected: $(cat "$work/err")"
		fi
		case "$expected" in
			'a.txt|before|one') touch "$build/watched/b.txt" ;;
			'a.txt,b.txt|before|one') printf after >"$build/payload" ;;
		esac
	done
	printf '%s\n' 'SHELL = "/bin/sh"' 'rule ./shell-output' $'\t@if true' \
		$'\tprintf "%s" "$(printf pass)" > @{first outputs}' $'\t@end if' >"$build/Kamefile"
	if (cd "$build" && "${runner[@]}" --allow-read --allow-write --allow-run ./shell-output) >"$work/out" 2>"$work/err" &&
		[ "$(cat "$build/shell-output")" = pass ]; then
		test-ok "$backend passes command substitution to the foreign shell"
	else
		test-fail "$backend foreign shell document: $(cat "$work/err")"
	fi
	printf '%s\n' 'SHELL = "/bin/sh"' 'rule ./heredoc' $'\tcat > @{outputs.0} <<\'END\'' \
		$'\tfirst' '' $'\t  ' '' $'\t@{nil}' $'\tlast' $'\tEND' >"$build/Kamefile"
	printf 'first\n\n  \n\n\nlast\n' >"$work/expected"
	if (cd "$build" && "${runner[@]}" --allow-read --allow-write --allow-run ./heredoc) >"$work/out" 2>"$work/err" &&
		cmp -s "$build/heredoc" "$work/expected"; then
		test-ok "$backend preserves literal and interpolated empty heredoc lines"
	else
		test-fail "$backend foreign shell heredoc: $(cat "$work/err")"
	fi
	cat >"$build/Kamefile" <<'KAME'
SHELL = "/bin/sh"
rule ./document-heredoc
	@if true
	cat > @{outputs.0} <<'END'
	  first
	@raw
	@{missing}
	@end raw
	END
	@else
	exit 99
	@end if
KAME
	printf '  first\n@{missing}\n' >"$work/expected"
	if (cd "$build" && "${runner[@]}" --allow-read --allow-write --allow-run ./document-heredoc) >"$work/out" 2>"$work/err" &&
		cmp -s "$build/document-heredoc" "$work/expected"; then
		test-ok "$backend strips rule indentation and preserves branch and raw text bytes"
	else
		test-fail "$backend foreign shell document heredoc: $(cat "$work/err")"
	fi
	cat >"$build/Kamefile" <<'KAME'
SHELL = "/bin/sh"
rule ./multiline-output
	printf '%s' "@{match "ready"
		"ready" => "matched"
	}" > @{outputs.0}
KAME
	if (cd "$build" && "${runner[@]}" --allow-read --allow-write --allow-run ./multiline-output) >"$work/out" 2>"$work/err" &&
		[ "$(cat "$build/multiline-output")" = matched ]; then
		test-ok "$backend evaluates multiline expressions in foreign shell interpolation"
	else
		test-fail "$backend foreign shell multiline interpolation: $(cat "$work/err")"
	fi
	cat >"$build/Kamefile" <<'KAME'
SHELL = "/bin/sh"
task inspect
	@raw
	printf '%s' '@{x/kame-template-raw-tool}'
	@end raw
	printf '%s' '@{x/sh}'
KAME
	if "${runner[@]}" do tools check --json -C "$build" inspect >"$work/out" 2>"$work/err" &&
		jq -es 'any(.[]; .type == "tools-check-result" and [.tools[].name] == ["sh"])' "$work/out" >/dev/null; then
		test-ok "$backend checks the interpolated tool and skips raw text"
	else
		test-fail "$backend document tool check: $(cat "$work/out" "$work/err")"
	fi
	if "${runner[@]}" do plan --json -C "$build" inspect >"$work/out" 2>"$work/err" &&
		jq -e 'any(.resources[]; .key.kind == "tool" and .display == "sh") and
			all(.resources[]; .display != "kame-template-raw-tool")' "$work/out" >/dev/null; then
		test-ok "$backend plan exposes the interpolated tool only"
	else
		test-fail "$backend document tool plan: $(cat "$work/out" "$work/err")"
	fi

	test-step "$backend rendering types and malformed directives"
	check '"./input"' do run --lang expr -c 'render "./input"'
	check '"exists"' do run --lang expr --allow-read -c "render (read $work/input)"
	check '"Ada"' do run --lang expr --allow-read -c "render {name: \"Ada\"} $work/child.md"
	reject CAP_DENIED do run --lang expr -c "render $work/child.md"
	reject TPL_STYLE do run --lang expr --allow-read -c "render $work/input"
	check '"exists"' do run --lang expr --allow-read -c "render \"plain\" $work/input"
	check '' do render --check -c "@if exists? $work/input"$'\nyes\n@end'
	reject CAP_DENIED do render -c "@if exists? $work/input"$'\nyes\n@end'
	reject TPL_PARSE do render -c $'@if\nyes\n@end'
	reject TPL_PARSE do render -c $'@if eq "a""b"\nyes\n@end'
	reject TPL_PARSE do render -c $'@for item in list "a" sep ,\nyes\n@end'
	reject TPL_BLOCK do render -c $'@if false\nyes'
	reject TPL_PARSE do render -c '@end if extra'
	reject TPL_BLOCK do render -c $'@if true\n@end with'
	reject TPL_PARSE do render -c '@else-'
	reject TPL_PARSE do render -c $'@match "./a"\n@case ./{\n@end match'
	reject PAT_INVALID do render -c $'@match "./a"\n@case ./{_1}\n@end match'
	reject EXPR_INVALID do render -c $'@for x in {a: 1}\n@{fail! "loop body should remain unevaluated"}\n@end for'
	reject EXPR_INVALID do render -c $'@for x y in [1]\n@{x}\n@end for'
	reject EXPR_INVALID do render -c $'@with 1\ntext\n@end with'
	reject EXPR_INVALID do render -c '@{[1, 2]}'
	reject EXPR_INVALID do render -c '@{{name: "Ada"}}'
	check safe do render -c $'@if false\n@if fail! "unselected"\nbad\n@end if\n@else -\n safe\n@-end if'

	test-step "$backend comment styles and idempotent directive formatting"
	for style in plain html c hash dash semi percent powershell batch; do
		case "$style" in
			plain) open='' close='' ;;
			html) open='<!-- ' close=' -->' ;;
			c) open='/* ' close=' */' ;;
			hash) open='# ' close='' ;;
			dash) open='-- ' close='' ;;
			semi) open='; ' close='' ;;
			percent) open='% ' close='' ;;
			powershell) open='<# ' close=' #>' ;;
			batch) open='REM ' close='' ;;
		esac
		check yes do render --comment "$style" -c "$open@if \"x\" == \"x\"$close"$'\nyes\n'"$open@end if$close"
		printf '%s\n' "$open@if   (\"x\"  ==  \"x\")$close" yes "$open@end$close" >"$work/source.tmpl"
		"${runner[@]}" do fmt --lang template --comment "$style" "$work/source.tmpl" >"$work/formatted"
		printf '%s\n' "$open@if (\"x\" == \"x\")$close" yes "$open@end if$close" >"$work/expected"
		"${runner[@]}" do fmt --lang template --comment "$style" "$work/formatted" >"$work/again"
		if cmp -s "$work/formatted" "$work/expected" && cmp -s "$work/formatted" "$work/again"; then test-ok "$backend $style canonical directive"; else test-fail "$backend $style formatting"; fi
	done

	check 'prefix @{str "A" "B"} suffix' do fmt --lang template --comment plain < <(printf '%s' 'prefix @{ str   "A"   "B" } suffix')

	test-step "$backend full expression interpolation and literal bytes"
	check '"Ada"' do run --lang expr -c '(name => "${name}") "Ada"'
	check '"ADA"' do run --lang expr -c '"${"Ada" |> uppercase}"'
	check '"v1"' do run --lang expr -c '(version => "${version ()}") (() => "v1")'
	check '"Ada"' do run --lang expr -c '(project => "${project.name}") {name: "Ada"}'
	check true do run --lang expr -c '"Values ${["a", [2, true], ./dir]}" == "Values a 2 true ./dir"'
	check true do run --lang expr -c '"pre${[]}|${nil}|${{key: 1}}|${x => x}post" == "pre||{key: 1}|<callable>post"'
	check Ada do render --define name=Ada -c '@{name}'
	check '${name} Ada' do render --define name=Ada -c '${name} @{name}'
	check xy do render -c '@{join "" ["x", "y"]}'
	check 42 do render -c '@{(x => x + 1) 41}'
	check 'item-a,item-b' do render -c '@{join "," (map (str "item-" _) ["a", "b"])}'
	reject EXPR_INVALID do render -c '@{(x => x)}'
	if grep -Fq 'cannot render callable' "$work/err" && grep -Fq 'Invoke the callable explicitly' "$work/err"; then
		test-ok "$backend names the callable kind and suggests an explicit call"
	else
		test-fail "$backend callable interpolation advice: $(cat "$work/err")"
	fi
	printf '\377' >"$work/invalid-utf8"
	reject EXPR_INVALID do render --allow-read -c "@{read $work/invalid-utf8}"
	if grep -Fq 'cannot render bytes' "$work/err"; then
		test-ok "$backend names the bytes kind"
	else
		test-fail "$backend bytes interpolation diagnostic: $(cat "$work/err")"
	fi
	reject EXPR_INVALID do run --lang expr --allow-read -c "text (read $work/invalid-utf8)"
	reject EXPR_INVALID do run --lang expr -c 'template-cat [1]'
	reject EXPR_INVALID do run --lang expr --allow-read -c "template-cat (read $work/invalid-utf8)"
	check_bytes $'@{missing}\n' do render -c $'@@{missing}\n'
	check 'value=@{missing}' do render -c 'value=@@{missing}'
	check '@if "x" == "x"' do render -c '@@if "x" == "x"'
	check 'literal @media @iffy @example.com' do render -c 'literal @media @iffy @example.com'
	check 'prefix @if true yes @end if' do render -c 'prefix @if true yes @end if'
	check_bytes $'hello @{missing}\n@end if\n' do render -c $'@raw\nhello @{missing}\n@end if\n@end raw'
	reject PARSE_ERR do render -c '@{str "unclosed"'
	reject REF_MISSING do render -c '\@{missing}'
	printf '%s\n' '@@if "x" == "x"' '@raw' '@{missing} @if "x" == "x"' '@end raw' >"$work/raw.tmpl"
	"${runner[@]}" do fmt --lang template --comment plain "$work/raw.tmpl" >"$work/raw.formatted"
	if cmp -s "$work/raw.tmpl" "$work/raw.formatted"; then test-ok "$backend formatting preserves raw and escaped directives"; else test-fail "$backend raw formatting"; fi
	test-step "$backend canonical value source and eval"
	canonical='{"two words": [1, 2.0, -0.0, nil], at: "@{inert}"}'
	check_bytes "$canonical" do run --lang expr -c '{ "two words": [1,2.0,-0.0,nil], at:"@{inert}" }'
	check_bytes "$canonical" do run --lang expr -c "$canonical"
	value_project="$work/value-$backend"
	mkdir -p "$value_project"
	printf 'displayed = %s\n' "$canonical" >"$value_project/Kamefile"
	check_bytes "$canonical" -C "$value_project" displayed
	printf '\000\377A\n' >"$work/binary"
	"${runner[@]}" do run --lang expr --allow-read -c "read $work/binary" >"$work/out" 2>"$work/err"
	if cmp -s "$work/out" "$work/binary"; then test-ok "$backend prints byte results verbatim"; else test-fail "$backend byte result changed"; fi
	check true do run --lang expr -c 'eval (fmt {name: [nil, true, -3, 2.0], "odd key": "value"}) == {name: [nil, true, -3, 2.0], "odd key": "value"}'
	check true do run --lang expr -c 'float? (eval (fmt -0.0))'
	check true do run --lang expr -c 'eval (fmt "./@{inert}") == "./@{inert}"'
	check true do run --lang expr -c 'get "name" (capture (eval (fmt ./src/{name:*}.c)) ./src/read.c) == "read"'
	check true do run --lang expr -c 'bind (eval (fmt ./out/{name}.c)) {name: "read"} == ./out/read.c'
	check 42 do run --lang expr -c '(x => std/eval "x + 1") 41'
	reject EXPR_INVALID do run --lang expr -c 'fmt {nested: [(x => x)]}'
	reject PARSE_ERR do run --lang expr -c 'eval "out! \"unreached\"\n42"'
	reject CAP_DENIED do run --lang expr -c "eval 'read $work/input'"
	check '"exists"' do run --lang expr --allow-read -c "eval 'text (read $work/input)'"
done
test-end
