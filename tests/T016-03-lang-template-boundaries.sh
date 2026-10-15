#!/usr/bin/env bash
# Spec: docs/spec/016-templates.md — directive boundaries and interpolation migration
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T016-03 directive boundaries and canonical interpolation"
test-step "build both hosts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/boundaries"
mkdir -p "$work"
printf exists >"$work/input"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend rejection ($status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend single-expression directives and explicit multi-argument slots"
	check yes do render --comment plain --allow-read -c "@if(exists? $work/input)yes@else no@end(if)"
	check yes do render --comment plain --allow-read -c "@if((exists? $work/input))yes@else no@end(if)"
	check yes do render --comment plain --define flag=yes -c '@if(flag)yes@else no@end(if)'
	check yes do render --comment plain -c '@if(:false)no@elif(eq "x" "x")yes@end(if)'
	check yes do render --comment plain -c '@if(([x] (eq x "yes")) "yes")yes@end(if)'
	check Ada do render --comment plain -c '@with(nop [name: "Ada"])@(name)@end(with)'
	check yes do render --comment plain -c '@match(str "a" "b")@case(str "a" "b")yes@else no@end(match)'
	check_bytes $'a\nb\n' do render --comment plain -c $'@for([item] (list "a" "b"))\n@(item)\n@end(for)'
	check_bytes $'a\n,b\n' do render --comment plain -c $'@for([item] (list "a" "b") ",")\n@(item)\n@end(for)'
	check Ada do render --comment plain -c $'@let(name (str "A" "da"))\n@(name)'
	printf '@(name)' >"$work/child.md"
	check Ada do render --comment plain --allow-read -c "@include((str \"$work/\" \"child.md\") [name: \"Ada\"])"
	printf '<!-- @include("%s/child.md") -->\n' "$work" >"$work/second.md"
	printf '<!-- @include("%s/second.md") -->\n' "$work" >"$work/first.md"
	check Ada do render --comment plain --allow-read -c "@include(\"$work/first.md\" [name: \"Ada\"])"
	printf '<!-- @include("%s/cycle.md") -->\n' "$work" >"$work/cycle.md"
	reject TPL_CYCLE do render --comment plain --allow-read -c "@include(\"$work/cycle.md\")"
	check '"yes"' do run --lang expr -c '(render """@if(eq "a" "a")yes@end(if)""")'
	check '"yes"' do run --lang expr -c '(let [predicate ([] :true)] (render """@if((predicate))yes@end(if)"""))'
	test-step "$backend malformed boundaries and lazy branches"
	check '' do render --comment plain --check -c "@if(exists? $work/input)yes@end(if)"
	reject CAP_DENIED do render --comment plain -c "@if(exists? $work/input)yes@end(if)"
	reject TPL_PARSE do render --comment plain -c $'@if()\nyes\n@end'
	reject TPL_PARSE do render --comment plain -c $'@if(eq "a""b")\nyes\n@end'
	reject TPL_PARSE do render --comment plain -c $'@for([item] (list "a") "," extra)\nyes\n@end'
	reject TPL_BLOCK do render --comment plain -c $'@if(eq "a" "b")\nyes'
	reject TPL_PARSE do render --comment plain -c $'@end(if extra)'
	check safe do render --comment plain -c '@if(:false)@if(fail! "unselected")bad@end(if)@else-safe@end(if)'
	check $'@if (eq "x" "x")\n@media literal' do render --comment plain -c $'@if (eq "x" "x")\n@media literal'
	test-step "$backend host comment styles and canonical formatting"
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
		check yes do render --comment "$style" -c "${open}@if(eq \"x\" \"x\")${close}"$'\nyes\n'"${open}@end(if)${close}"
		printf '%s\n' "${open}@if((eq \"x\" \"x\"))${close}" yes "${open}@end${close}" >"$work/source.tmpl"
		"${runner[@]}" do fmt --lang template --comment "$style" "$work/source.tmpl" >"$work/formatted"
		printf '%s\n' "${open}@if(eq \"x\" \"x\")${close}" yes "${open}@end(if)${close}" >"$work/expected"
		"${runner[@]}" do fmt --lang template --comment "$style" "$work/formatted" >"$work/again"
		if cmp -s "$work/formatted" "$work/expected" && cmp -s "$work/formatted" "$work/again"; then test-ok "$backend $style canonical directive"; else test-fail "$backend $style formatting"; fi
	done
	test-step "$backend interpolation migration and inert text"
	check '" (str [VALUE]) "' do run --lang expr -c '(str "@(" (str [VALUE]) ")")'
	check '"@(hello)"' do run --lang expr -c '(str """@(""" "hello" ")")'
	reject PARSE_ERR do run --lang expr -c '"@("'
	reject EVAL_DEPTH do run -c '(walk TEXT) = (if (= (count TEXT) 0) 0 (character TEXT))' -c '(character TEXT) = (walk (slice 1 TEXT))' -c '(walk (reduce ([TEXT STEP] (str TEXT TEXT)) "x,y\n" [1 2 3 4 5 6 7 8 9 10]))'
	check '"value=xy"' do run --lang expr -c '"value=@(join "" ["x" "y"])"'
	check '"value=xy"' do run --lang expr -c '"value={(join "" ["x" "y"])}"'
	check value=xy do run --lang kash -c 'value = "value={(join "" ["x" "y"])}"; printf "%s" $value'
	check '"outer x2y end"' do run --lang expr -c '"outer {(str "x{(count [1 2])}y")} end"'
	check '"@(missing) {(missing)}"' do run --lang expr -c '"\@(missing) \{(missing)}"'
	check '"@(missing) {(missing)}"' do run --lang expr -c '"""@(missing) {(missing)}"""'
	check '@(missing) {(missing)}' do render --comment plain -c $'@raw\n@(missing) {(missing)}\n@end(raw)'
	check '@(missing) {(missing)}' do render --comment plain -c '\@(missing) {(missing)}'
	check '@if((eq "x" "x"))' do render --comment plain -c $'@@if((eq "x" "x"))'
	check '@if(eq "x" "x")' do render --comment plain -c '\@if(eq "x" "x")'
	printf '%s\n' '@@if((eq "x" "x"))' '\@if(eq "x" "x")' '@raw' '@(missing) {(missing)} @if((eq "x" "x"))' '@end(raw)' >"$work/raw.tmpl"
	"${runner[@]}" do fmt --lang template --comment plain "$work/raw.tmpl" >"$work/raw.formatted"
	if cmp -s "$work/raw.tmpl" "$work/raw.formatted"; then test-ok "$backend formatting preserves raw and escaped directives"; else test-fail "$backend raw formatting"; fi
	reject PARSE_ERR do run --lang expr -c '"bad @(str "x""y")"'
	reject PARSE_ERR do run --lang expr -c '"bad {(count [1 2])"'
	printf '%s\n' 'VALUE = "outer {(str "x{(count [1 2])}y")} end"' 'RAW = """@(missing) {(missing)}"""' 'ESCAPED = "\@(missing) \{(missing)}"' >"$work/migration.km"
	"${runner[@]}" do fmt --lang km "$work/migration.km" >"$TMPDIR/$backend.migrated"
	"${runner[@]}" do fmt --lang km "$TMPDIR/$backend.migrated" >"$work/again"
	if grep -Fq 'VALUE = "outer @(str "x@(count [1 2])y") end"' "$TMPDIR/$backend.migrated" && grep -Fq 'ESCAPED = "\@(missing) \{(missing)}"' "$TMPDIR/$backend.migrated" && cmp -s "$TMPDIR/$backend.migrated" "$work/again"; then test-ok "$backend interpolation migration is idempotent"; else test-fail "$backend interpolation migration"; cat "$TMPDIR/$backend.migrated"; fi
	check '"outer x2y end"' do run --lang km -f "$TMPDIR/$backend.migrated" VALUE
	for lang in kmk kash; do
		"${runner[@]}" do fmt --lang "$lang" "$work/migration.km" >"$work/$lang.migrated"
		if cmp -s "$TMPDIR/$backend.migrated" "$work/$lang.migrated"; then test-ok "$backend $lang expression interpolation migration"; else test-fail "$backend $lang interpolation migration"; fi
	done
	status=0
	"${runner[@]}" do fmt --lang km --check "$work/migration.km" >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ]; then test-ok "$backend check detects deprecated interpolation"; else test-fail "$backend migration check"; fi
	"${runner[@]}" do fmt --lang km --in-place "$work/migration.km" >"$work/out" 2>"$work/err"
	check '' do fmt --lang km --check "$work/migration.km"
	cat >"$work/Makefile.kmk" <<'KMK'
./output : ./input
	@if(exists? ./input)
	printf selected > @>
	@else
	printf forbidden > @>
	@end(if)
KMK
	rm -f "$work/output"
	check '' do run -C "$work" "$work/Makefile.kmk" ./output
	if [ "$(cat "$work/output")" = selected ]; then test-ok "$backend external-shell recipe directive shorthand"; else test-fail "$backend recipe boundary"; fi
	printf '%s\n' '@if((eq "x" "x"))yes@end' >"$work/parity.tmpl"
	"${runner[@]}" do fmt --lang template --comment plain "$work/parity.tmpl" >"$TMPDIR/$backend.directives"
done
for kind in migrated directives; do if cmp -s "$TMPDIR/native.$kind" "$TMPDIR/wasm.$kind"; then test-ok "$kind host parity"; else test-fail "$kind host parity"; fi; done
test-end
