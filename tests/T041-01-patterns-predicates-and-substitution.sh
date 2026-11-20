#!/usr/bin/env bash
# Spec: docs/spec/041-pattern-predicates-and-substitution.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T041-01 patterns predicates and substitution"
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}

for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	for sample in \
		'(match? ./{*}.{*} ./foo.txt)|true' \
		'(match? ./{*}.{*} ./foo)|false' \
		'(match? ./{*}.{*} ./.txt)|false' \
		'((match? (pattern "{name:~[a-z]+}")) "demo")|true' \
		'[(match? ./src/{**}/{*}.c ./src/lib/demo.c), ((match? ./src/{**}/{*}.c) ./src/lib/demo.c), (match? ./src/{**}/{*}.c "./src/lib/demo.h"), ((match? ./src/{**}/{*}.c) "./src/lib/demo.h")]|[true, true, false, false]' \
		'(map (match? (pattern "{name:*}-{name:*}")) ["a-a", "a-b"])|[true, false]' \
		'(filter-out (match? ./{*}.{*}) (map (subst ./journal/{name:*} ./{name}) [./journal/foo, ./journal/bar.txt]))|[./foo]' \
		'(filter (match? ./{*}.{*}) [./foo, ./foo.txt])|[./foo.txt]' \
		'(subst ./journal/{name:*} missing "./other/foo")|"./other/foo"' \
		'(subst ./journal/{name:*} missing ./other/foo)|./other/foo' \
		'(subst ./{name:*}.c ./{name}.o [./a.c, ./b.h])|[./a.o, ./b.h]' \
		'(subst "a" "b" "banana")|"bbnbnb"' \
		'(subst ./{*}.c "x" [])|[]' \
		'(subst ./{*}.c "x" nil)|nil' \
		'(resub (pattern "{name:~[a-z]+}") "x" "42")|nil'; do
		"${runner[@]}" do run --lang expr -c "${sample%|*}" >"$TMPDIR/value" 2>"$TMPDIR/error"
		if [ "$(cat "$TMPDIR/value")" = "${sample##*|}" ] && [ ! -s "$TMPDIR/error" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend ${sample%|*}: $(cat "$TMPDIR/value")"; fi
	done
	for expression in '(match? (pattern "{*}") nil)' '(match? (pattern "{*}") ["a"])' '((match? (pattern "{*}")) 42)' '(match? "a" "a")' '(match? ./{name})' '((match? (pattern "plain")) nil)'; do
		status=0
		"${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/value" 2>"$TMPDIR/error" || status=$?
		if [ "$status" = 1 ] && grep -Eq 'EXPR_INVALID|PAT_INVALID' "$TMPDIR/error"; then test-ok "$backend rejects $expression"; else test-fail "$backend accepted $expression"; fi
	done
	for expression in '(subst "a" "b")' '(subst "a" 1 "a")' '(subst "a" "b" nil)' '(subst "a" ./{*} "a")'; do
		status=0
		"${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/value" 2>"$TMPDIR/error" || status=$?
		if [ "$status" = 1 ] && grep -Fq EXPR_INVALID "$TMPDIR/error"; then test-ok "$backend rejects literal $expression"; else test-fail "$backend accepted literal $expression"; fi
	done
	test-step "$backend discovers substitution subjects"
	work="$TMPDIR/$backend"
	mkdir -p "$work/alpha/.jj" "$work/beta repo/.jj" "$work/unrelated"
	printf 'payload' >"$work/alpha/data.txt"
	for expression in 'subst ./{path:*}/.jj ./{path} ./*/.jj' '((subst ./{path:*}/.jj ./{path}) ./*/.jj)'; do
		"${runner[@]}" do run -C "$work" --allow-read="$work" --lang expr -c "$expression" >"$work/value"
		if [ "$(cat "$work/value")" = '[./alpha, ./beta\ repo]' ]; then
			test-ok "$backend expands a pattern subject"
		else
			test-fail "$backend pattern subject returned $(cat "$work/value")"
		fi
	done
	"${runner[@]}" do run -C "$work" --allow-read --lang expr -c 'subst ./{name:*}.txt (text (read "./alpha/${name}.txt")) ./alpha/*.txt' >"$work/value"
	if [ "$(cat "$work/value")" = '[./alpha/data.txt]' ]; then
		test-ok "$backend keeps a nonmatching discovered path"
	else
		test-fail "$backend unexpected nonmatch $(cat "$work/value")"
	fi
	"${runner[@]}" do run -C "$work" --allow-read --lang expr -c 'subst ./alpha/{name:*}.txt (text (read "./alpha/${name}.txt")) ./alpha/*.txt' >"$work/value"
	if [ "$(cat "$work/value")" = '["payload"]' ]; then test-ok "$backend resumes discovery and replacement reads"; else test-fail "$backend replacement read"; fi
	"${runner[@]}" do run -C "$work" --allow-read --lang expr -c 'subst ./{path:*}/.jj ./{path} ./absent*/.jj' >"$work/value"
	if [ "$(cat "$work/value")" = '[]' ]; then test-ok "$backend empty discovery"; else test-fail "$backend empty discovery"; fi
	status=0
	"${runner[@]}" do run -C "$work" --allow-read="$work/unrelated" --lang expr -c 'subst ./{path:*}/.jj ./{path} ./*/.jj' >"$work/value" 2>"$work/error" || status=$?
	if [ "$status" = 1 ] && grep -Fq CAP_DENIED "$work/error"; then test-ok "$backend preserves read scope"; else test-fail "$backend expected a read denial"; fi
	cat >"$work/Kamefile" <<'KAME'
REPOS = (subst ./{path:*}/.jj ./{path} ./*/.jj)
task all :
	echo "Repos ${REPOS}"
task paths : ${REPOS}
KAME
	"${runner[@]}" -C "$work" all >"$work/value"
	if [ "$(cat "$work/value")" = 'Repos ./alpha ./beta repo' ]; then test-ok "$backend repository task renders the paths"; else test-fail "$backend repository task"; fi
	"${runner[@]}" do plan --json -C "$work" paths >"$work/plan"
	if jq -e '[.resources[] | select(.roles | index("input")) | .display] | index("./*/.jj") != null and index("./alpha") != null and index("./beta repo") != null' "$work/plan" >/dev/null; then
		test-ok "$backend tracks the discovery and the resulting prerequisites"
	else
		test-fail "$backend plan did not retain the discovery"
	fi

	test-step "$backend formats a composed predicate and replacement idempotently"
	composition='[./journal/foo,./journal/bar.txt] |> map (subst ./journal/{name:*} ./{name}) |> filter-out (match? ./{*}.{*})'
	"${runner[@]}" do fmt --lang expr -c "$composition" >"$TMPDIR/formatted"
	"${runner[@]}" do fmt --lang expr "$TMPDIR/formatted" >"$TMPDIR/reformatted"
	"${runner[@]}" do run --lang expr -f "$TMPDIR/formatted" >"$TMPDIR/value"
	if cmp -s "$TMPDIR/formatted" "$TMPDIR/reformatted" && [ "$(cat "$TMPDIR/value")" = '[./foo]' ]; then
		test-ok "$backend preserves predicate and replacement composition"
	else
		test-fail "$backend changed predicate and replacement composition"
	fi
done
test-end
