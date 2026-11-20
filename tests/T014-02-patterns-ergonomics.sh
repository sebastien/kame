#!/usr/bin/env bash
# Spec: docs/spec/014-patterns.md — leading captures and scalar expansions
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T014-02 pattern ergonomics"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
project="$TMPDIR/project"
mkdir -p "$project"
cat >"$project/Makefile.km" <<'KM'
task {name:*}
	(out! name)
task exact
	(out! "literal")
task aws-shell@{role-account}
	(out! role-account)
KM
printf 'task aws-shell@{role-account}\n\t(out! role-account)\n' >"$project/named.km"
mkdir -p "$project/src/txto/blog/2026/deep"
touch "$project/src/txto/blog/foo.txto" "$project/src/txto/blog/2026/deep/foo.txto" "$project/src/txto/blog/skip.txto"
cat >"$project/replace.km" <<'KM'
SOURCES_TXTO = (wildcard ./src/txto/**/*.txto)
FILTERED = (filter-out (x => includes? "skip" (str x)) SOURCES_TXTO)
BUILD_JSON = map str (subst ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json FILTERED)
SECTION = (subst ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json)
rule ./result : ${SOURCES_TXTO} = join  "\n" BUILD_JSON
KM
cp "$project/replace.km" "$project/replace.original"
mkdir -p "$project/journal"
touch "$project/journal/a" "$project/journal/b" "$project/journal/star*"
cat >"$project/globs.km" <<'KM'
SOURCES_JOURNAL = files ./journal/*
PRODUCT_BY_NAME = (map (subst ./journal/{name:*} "${name}") SOURCES_JOURNAL)
PRODUCT_PATHS = (subst ./journal/{name:*} ./{name} SOURCES_JOURNAL)
KM
for backend in native wasm; do
	test-step "$backend leading expression patterns and scalar expansions"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	test-step "$backend glob expressions and capture-scoped replacements"
	for sample in \
		'(map (subst ./journal/{name:*} "${name}") (files ./journal/*))|["a", "b", "star*"]' \
		'(map (subst ./journal/{name:*} name) (files ./journal/*))|["a", "b", "star*"]' \
		'(subst ./journal/{name:*} ./{name} (files ./journal/*))|[./a, ./b, ./star\*]' \
		'(wildcard ./journal/*)|[./journal/a, ./journal/b, ./journal/star\*]' \
		'(wildcard :+files ./journal/*)|[./journal/a, ./journal/b, ./journal/star\*]' \
		'files ./journal/[ab]|[./journal/a, ./journal/b]' \
		'files ./journal/[a\]]|[./journal/a]' \
		'(name => files ./journal/[a${name}]) "b"|[./journal/a, ./journal/b]' \
		'files ./journal/a*|[./journal/a]' \
		'files ./absent/*|[]' \
		'"./journal/*"|"./journal/*"' \
		'./journal/star\*|./journal/star\*' \
		'(wildcard [./journal/star\*])|[./journal/star\*]' \
		'(name => ./journal/${name}) "star*"|./journal/star\*' \
		'(name => [(subst ./journal/{name:*} name "./journal/a"), name]) "outer"|["a", "outer"]' \
		'(subst ./journal/{name:*} missing "./other/a")|"./other/a"' \
		'(subst ./journal/{name:*} missing [])|[]'; do
		"${runner[@]}" do run --lang expr --allow-read -C "$project" -c "${sample%|*}" >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend glob/capture result for ${sample%|*}: $(cat "$project/value")"; fi
	done
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(subst ./journal/{name:*} (text (read ./journal/${name})) (files ./journal/*))' >"$project/value"
	if [ "$(cat "$project/value")" = '["", "", ""]' ]; then test-ok "$backend suspended capture reads"; else test-fail "$backend captured read replay"; fi
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(subst ./journal/{name:*} (nop (out! name) (text (read ./journal/${name}))) (files ./journal/*))' >"$project/value"
	if [ "$(cat "$project/value")" = 'abstar*["", "", ""]' ]; then test-ok "$backend replacement effects survive host yields once"; else test-fail "$backend replacement effect replay: $(cat "$project/value")"; fi
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(map (subst ./journal/{name:*} (nop (out! name) (text (read ./journal/${name})))) (files ./journal/*))' >"$project/value"
	if [ "$(cat "$project/value")" = 'abstar*["", "", ""]' ]; then test-ok "$backend mapped replacement effects survive host yields once"; else test-fail "$backend mapped replacement effect replay: $(cat "$project/value")"; fi
	for sample in \
		'(filter (subst ./journal/{name:*} (nop (out! name) (text (read ./journal/${name})))) (files ./journal/*))|abstar*[./journal/a, ./journal/b, ./journal/star\*]' \
		'(filter-out (subst ./journal/{name:*} (nop (out! name) (text (read ./journal/${name})))) (files ./journal/*))|abstar*[]' \
		'(reduce (acc subject => nop (subst ./journal/{name:*} (nop (out! name) (text (read ./journal/${name}))) subject) acc) "" (files ./journal/*))|abstar*""'; do
		"${runner[@]}" do run --lang expr --allow-read -C "$project" -c "${sample%|*}" >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend callback effects survive host yields once"; else test-fail "$backend callback effect replay: $(cat "$project/value")"; fi
	done
	status=0
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c 'files ./journal/[${missing}]' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q REF_MISSING "$project/error"; then test-ok "$backend class interpolation demands its reference"; else test-fail "$backend class interpolation skipped missing reference"; fi
	"${runner[@]}" do run --allow-read -C "$project" "$project/globs.km" PRODUCT_BY_NAME >"$project/value"
	touch "$project/journal/new"
	"${runner[@]}" do run --allow-read -C "$project" "$project/globs.km" PRODUCT_BY_NAME >"$project/value"
	if [ "$(cat "$project/value")" = '["a", "b", "new", "star*"]' ]; then test-ok "$backend added glob member"; else test-fail "$backend glob membership addition"; fi
	rm "$project/journal/new"
	"${runner[@]}" do run --allow-read -C "$project" "$project/globs.km" PRODUCT_BY_NAME >"$project/value"
	if [ "$(cat "$project/value")" = '["a", "b", "star*"]' ]; then test-ok "$backend lazy glob definition"; else test-fail "$backend glob definition"; fi
	"${runner[@]}" do run --lang expr -C "$project" -c 'if false then files ./journal/* else "skipped"' >"$project/value"
	if [ "$(cat "$project/value")" = '"skipped"' ]; then test-ok "$backend unreached glob needs no read grant"; else test-fail "$backend glob eagerly demanded"; fi
	status=0
	"${runner[@]}" do run --lang expr -C "$project" -c 'files ./journal/*' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$project/error"; then test-ok "$backend reached glob enforces read grant"; else test-fail "$backend unauthorized glob read"; fi
	"${runner[@]}" do run --lang kash --allow-run -C "$project" -c 'printf "%s" ./journal/*' >"$project/value"
	if [ "$(cat "$project/value")" = './journal/*' ]; then test-ok "$backend raw Kash glob word stays literal"; else test-fail "$backend raw Kash word expanded"; fi
	for sample in '(subst (pattern "{name:*}.c") 1 "hello.c")|"1"' '(subst (pattern "{*}.c") false "a.c")|"false"' '(subst (pattern "{*}.c") -2.5 "a.c")|"-2.5"' '(subst (pattern "{*}.c") 1 "a.h")|"a.h"' '(map (subst (pattern "{*}.c") true) ["a.c", "b.c"])|["true", "true"]' '(subst (pattern "{name:*}.c") (pattern "{name}.o") "hello.c")|"hello.o"'; do
		expression="${sample%|*}"
		expected="${sample##*|}"
		"${runner[@]}" do run --lang expr -c "$expression" >"$project/value" 2>"$project/error"
		if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend $expression"; else test-fail "$backend scalar pattern result"; fi
	done
	status=0
	"${runner[@]}" do run --lang expr -c '(subst (pattern "{*}.c") [1] "a.c")' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q PAT_INVALID "$project/error"; then test-ok "$backend collection expansion stays invalid"; else test-fail "$backend collection coercion"; fi
	test-step "$backend recursive replacement over filtered lazy wildcard lists"
	"${runner[@]}" do cat -C "$project" -f replace.km BUILD_JSON >"$project/value"
	expected='["./build/data/blog/2026/deep/foo.json","./build/data/blog/foo.json"]'
	if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend filtered wildcard replacement"; else test-fail "$backend filtered wildcard result: $(cat "$project/value")"; fi
	for sample in \
		'(subst ./{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json ["./src/txto/blog/2026/deep/foo.txto"])|[./build/data/src/txto/blog/2026/deep/foo.json]' \
		'(map (subst ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json) ["./src/txto/blog/2026/deep/foo.txto"])|[./build/data/blog/2026/deep/foo.json]' \
		'(subst ./src/txto/{prefix:**}.txto ./build/data/{prefix}.json ["./src/txto/root.txto", "./src/txto/blog/foo.txto"])|[./build/data/root.json, ./build/data/blog/foo.json]' \
		'(subst ./{name:*}.txto ./{name}.json [])|[]' \
		'(subst ./{name:*}.txto ./{name}.json ["./a.txto", "./b.js"])|[./a.json, "./b.js"]'; do
		"${runner[@]}" do run --lang expr -c "${sample%|*}" >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend replacement result: $(cat "$project/value")"; fi
	done
	status=0
	"${runner[@]}" do cat -C "$project" -f replace.km SECTION >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q EXPR_INVALID "$project/error"; then test-ok "$backend callable boundary remains explicit"; else test-fail "$backend callable boundary"; fi
	"${runner[@]}" do plan -C "$project" -f replace.km ./result >/dev/null
	"${runner[@]}" -C "$project" -f replace.km -n ./result >/dev/null 2>"$project/error"
	"${runner[@]}" -C "$project" -f replace.km ./result >/dev/null 2>"$project/error"
	"${runner[@]}" -C "$project" -f replace.km ./result >/dev/null 2>"$project/error"
	"${runner[@]}" do fmt --lang km "$project/replace.km" >"$project/formatted.km"
	"${runner[@]}" do fmt --lang km "$project/formatted.km" >"$project/formatted-again.km"
	status=0
	"${runner[@]}" do fmt --lang km -n "$project/replace.km" >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ]; then test-ok "$backend fmt check reports canonical style differences"; else test-fail "$backend fmt check status $status"; fi
	if cmp -s "$project/formatted.km" "$project/formatted-again.km" && cmp -s "$project/replace.km" "$project/replace.original"; then test-ok "$backend inspection/build/fmt preserve source bytes"; else test-fail "$backend source mutation or unstable formatting"; fi
	"${runner[@]}" do cat -C "$project" -f formatted.km BUILD_JSON >"$project/value"
	if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend formatted replacement evaluates identically"; else test-fail "$backend formatted replacement result"; fi
	for file in replace.km formatted.km; do
		"${runner[@]}" do parse --json --lang km "$project/$file" | jq -S 'walk(if type == "object" then del(.span, .start, .end) else . end) | del(.source)' >"$project/$file.ast"
	done
	if cmp -s "$project/replace.km.ast" "$project/formatted.km.ast"; then test-ok "$backend wildcard/replace formatting preserves AST"; else test-fail "$backend wildcard/replace formatting changed AST"; fi
	for sample in 'demo|demo' 'exact|literal' 'aws-shell@demo|demo'; do
		source_file=Makefile.km
		if [ "${sample%|*}" = aws-shell@demo ]; then source_file=named.km; fi
		"${runner[@]}" -C "$project" -f "$source_file" "${sample%|*}" >"$project/value" 2>"$project/error"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend capture target ${sample%|*}"; else test-fail "$backend bare capture or exact precedence"; fi
	done
	status=0
	"${runner[@]}" -C "$project" aws-shell@demo >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q TGT_AMBIG "$project/error"; then test-ok "$backend overlapping capture rules remain ambiguous"; else test-fail "$backend capture ambiguity"; fi
done
test-end
