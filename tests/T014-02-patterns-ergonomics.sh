#!/usr/bin/env bash
# Spec: docs/spec/014-patterns.md — leading captures and scalar expansions
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T014-02 pattern ergonomics"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'KMK'
{name:*} :
	@(out! name)
exact :
	@(out! "literal")
aws-shell@{role-account} :
	@(out! role-account)
KMK
printf 'aws-shell@{role-account} :\n\t@(out! role-account)\n' >"$project/named.kmk"
mkdir -p "$project/src/txto/blog/2026/deep"
touch "$project/src/txto/blog/foo.txto" "$project/src/txto/blog/2026/deep/foo.txto" "$project/src/txto/blog/skip.txto"
cat >"$project/replace.kmk" <<'KMK'
SOURCES_TXTO = (wildcard ./src/txto/**/*.txto)
FILTERED = (filter-out ([x] (includes? x "skip")) SOURCES_TXTO)
BUILD_JSON = (replace ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json FILTERED)
SECTION = (replace ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json)
./result : @(SOURCES_TXTO)
	@(yield! (join BUILD_JSON "\n"))
KMK
cp "$project/replace.kmk" "$project/replace.original"
mkdir -p "$project/journal"
touch "$project/journal/a" "$project/journal/b" "$project/journal/star*"
cat >"$project/globs.km" <<'KM'
SOURCES_JOURNAL = ./journal/*
PRODUCT_BY_NAME = (map (replace ./journal/{name:*} "@(name)") SOURCES_JOURNAL)
PRODUCT_PATHS = (replace ./journal/{name:*} ./{name} SOURCES_JOURNAL)
KM
for backend in native wasm; do
	test-step "$backend leading expression patterns and scalar expansions"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend glob expressions and capture-scoped replacements"
	for sample in \
		'(map (replace ./journal/{name:*} "@(name)") ./journal/*)|["a" "b" "star*"]' \
		'(map (replace ./journal/{name:*} name) ./journal/*)|["a" "b" "star*"]' \
		'(replace ./journal/{name:*} ./{name} ./journal/*)|["./a" "./b" "./star*"]' \
		'(wildcard ./journal/*)|["./journal/a" "./journal/b" "./journal/star*"]' \
		'(wildcard :+files ./journal/*)|["./journal/a" "./journal/b" "./journal/star*"]' \
		'./journal/[ab]|["./journal/a" "./journal/b"]' \
		'./journal/[a\]]|["./journal/a"]' \
		'(let [name "b"] ./journal/[a@(name)])|["./journal/a" "./journal/b"]' \
		'./journal/a*|["./journal/a"]' \
		'./absent/*|[]' \
		'"./journal/*"|"./journal/*"' \
		'./journal/star\*|"./journal/star*"' \
		'(wildcard ./journal/star\*)|["./journal/star*"]' \
		'(let [name "star*"] ./journal/@(name))|"./journal/star*"' \
		'(let [name "outer"] [(replace ./journal/{name:*} name "./journal/a") name])|["a" "outer"]' \
		'(replace ./journal/{name:*} missing "./other/a")|:nil' \
		'(replace ./journal/{name:*} missing [])|[]'; do
		"${runner[@]}" do run --lang expr --allow-read -C "$project" -c "${sample%|*}" >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend glob/capture result"; fi
	done
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(replace ./journal/{name:*} (text (read ./journal/@(name))) ./journal/*)' >"$project/value"
	if [ "$(cat "$project/value")" = '["" "" ""]' ]; then test-ok "$backend suspended capture reads"; else test-fail "$backend captured read replay"; fi
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(replace ./journal/{name:*} (nop (out! name) (text (read ./journal/@(name)))) ./journal/*)' >"$project/value"
	if [ "$(cat "$project/value")" = 'abstar*["" "" ""]' ]; then test-ok "$backend replacement effects survive host yields once"; else test-fail "$backend replacement effect replay: $(cat "$project/value")"; fi
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c '(map (replace ./journal/{name:*} (nop (out! name) (text (read ./journal/@(name))))) ./journal/*)' >"$project/value"
	if [ "$(cat "$project/value")" = 'abstar*["" "" ""]' ]; then test-ok "$backend mapped replacement effects survive host yields once"; else test-fail "$backend mapped replacement effect replay: $(cat "$project/value")"; fi
	for sample in \
		'(filter (replace ./journal/{name:*} (nop (out! name) (text (read ./journal/@(name))))) ./journal/*)|abstar*["./journal/a" "./journal/b" "./journal/star*"]' \
		'(filter-out (replace ./journal/{name:*} (nop (out! name) (text (read ./journal/@(name))))) ./journal/*)|abstar*[]' \
		'(reduce ([acc subject] (nop (replace ./journal/{name:*} (nop (out! name) (text (read ./journal/@(name)))) subject) acc)) ./journal/* "")|abstar*""'; do
		"${runner[@]}" do run --lang expr --allow-read -C "$project" -c "${sample%|*}" >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend callback effects survive host yields once"; else test-fail "$backend callback effect replay: $(cat "$project/value")"; fi
	done
	status=0
	"${runner[@]}" do run --lang expr --allow-read -C "$project" -c './journal/[@(missing)]' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q REF_MISSING "$project/error"; then test-ok "$backend class interpolation demands its reference"; else test-fail "$backend class interpolation skipped missing reference"; fi
	"${runner[@]}" do run --allow-read -C "$project" "$project/globs.km" PRODUCT_BY_NAME >"$project/value"
	touch "$project/journal/new"
	"${runner[@]}" do run --allow-read -C "$project" "$project/globs.km" PRODUCT_BY_NAME >"$project/value"
	if [ "$(cat "$project/value")" = '["a" "b" "new" "star*"]' ]; then test-ok "$backend added glob member"; else test-fail "$backend glob membership addition"; fi
	rm "$project/journal/new"
	"${runner[@]}" do run --allow-read -C "$project" "$project/globs.km" PRODUCT_BY_NAME >"$project/value"
	if [ "$(cat "$project/value")" = '["a" "b" "star*"]' ]; then test-ok "$backend lazy glob definition"; else test-fail "$backend glob definition"; fi
	"${runner[@]}" do run --lang expr -C "$project" -c '(if :false ./journal/* "skipped")' >"$project/value"
	if [ "$(cat "$project/value")" = '"skipped"' ]; then test-ok "$backend unreached glob needs no read grant"; else test-fail "$backend glob eagerly demanded"; fi
	status=0
	"${runner[@]}" do run --lang expr -C "$project" -c './journal/*' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$project/error"; then test-ok "$backend reached glob enforces read grant"; else test-fail "$backend unauthorized glob read"; fi
	"${runner[@]}" do run --lang kash --allow-run -C "$project" -c 'printf "%s" ./journal/*' >"$project/value"
	if [ "$(cat "$project/value")" = './journal/*' ]; then test-ok "$backend raw Kash glob word stays literal"; else test-fail "$backend raw Kash word expanded"; fi
	for sample in '(replace {name:*}.c 1 "hello.c")|"1"' '(replace {*}.c :false "a.c")|"false"' '(replace {*}.c -2.5 "a.c")|"-2.5"' '(replace {*}.c 1 "a.h")|:nil' '(map (replace {*}.c :true) ["a.c" "b.c"])|["true" "true"]' '(replace {name:*}.c {name}.o "hello.c")|"hello.o"'; do
		expression="${sample%|*}"
		expected="${sample##*|}"
		"${runner[@]}" do run --lang expr -c "$expression" >"$project/value" 2>"$project/error"
		if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend $expression"; else test-fail "$backend scalar pattern result"; fi
	done
	status=0
	"${runner[@]}" do run --lang expr -c '(replace {*}.c [1] "a.c")' >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q PAT_INVALID "$project/error"; then test-ok "$backend collection expansion stays invalid"; else test-fail "$backend collection coercion"; fi
	test-step "$backend recursive replacement over filtered lazy wildcard lists"
	"${runner[@]}" do cat -C "$project" -f replace.kmk BUILD_JSON >"$project/value"
	expected='["./build/data/blog/2026/deep/foo.json","./build/data/blog/foo.json"]'
	if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend filtered wildcard replacement"; else test-fail "$backend filtered wildcard result: $(cat "$project/value")"; fi
	for sample in \
		'(replace ./{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json ["./src/txto/blog/2026/deep/foo.txto"])|["./build/data/src/txto/blog/2026/deep/foo.json"]' \
		'(map (replace ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json) ["./src/txto/blog/2026/deep/foo.txto"])|["./build/data/blog/2026/deep/foo.json"]' \
		'(replace ./src/txto/{prefix:**}.txto ./build/data/{prefix}.json ["./src/txto/root.txto" "./src/txto/blog/foo.txto"])|["./build/data/root.json" "./build/data/blog/foo.json"]' \
		'(replace ./{name:*}.txto ./{name}.json [])|[]' \
		'(replace ./{name:*}.txto ./{name}.json ["./a.txto" "./b.js"])|["./a.json" :nil]'; do
		"${runner[@]}" do run --lang expr -c "${sample%|*}" >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend replacement result: $(cat "$project/value")"; fi
	done
	status=0
	"${runner[@]}" do cat -C "$project" -f replace.kmk SECTION >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q EXPR_INVALID "$project/error"; then test-ok "$backend callable boundary remains explicit"; else test-fail "$backend callable boundary"; fi
	"${runner[@]}" do plan -C "$project" -f replace.kmk ./result >/dev/null
	"${runner[@]}" -C "$project" -f replace.kmk -n ./result >/dev/null 2>"$project/error"
	"${runner[@]}" -C "$project" -f replace.kmk ./result >/dev/null 2>"$project/error"
	"${runner[@]}" -C "$project" -f replace.kmk ./result >/dev/null 2>"$project/error"
	"${runner[@]}" do fmt --lang script "$project/replace.kmk" >"$project/formatted.kmk"
	"${runner[@]}" do fmt --lang script "$project/formatted.kmk" >"$project/formatted-again.kmk"
	status=0
	"${runner[@]}" do fmt --lang script -n "$project/replace.kmk" >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ]; then test-ok "$backend fmt check reports canonical style differences"; else test-fail "$backend fmt check status $status"; fi
	if cmp -s "$project/formatted.kmk" "$project/formatted-again.kmk" && cmp -s "$project/replace.kmk" "$project/replace.original"; then test-ok "$backend inspection/build/fmt preserve source bytes"; else test-fail "$backend source mutation or unstable formatting"; fi
	"${runner[@]}" do cat -C "$project" -f formatted.kmk BUILD_JSON >"$project/value"
	if [ "$(cat "$project/value")" = "$expected" ]; then test-ok "$backend formatted replacement evaluates identically"; else test-fail "$backend formatted replacement result"; fi
	for file in replace.kmk formatted.kmk; do
		"${runner[@]}" do parse --json --lang script "$project/$file" | jq -S 'walk(if type == "object" then del(.span, .start, .end) else . end) | del(.source)' >"$project/$file.ast"
	done
	if cmp -s "$project/replace.kmk.ast" "$project/formatted.kmk.ast"; then test-ok "$backend wildcard/replace formatting preserves AST"; else test-fail "$backend wildcard/replace formatting changed AST"; fi
	for sample in 'demo|demo' 'exact|literal' 'aws-shell@demo|demo'; do
		source_file=Makefile.kmk
		if [ "${sample%|*}" = aws-shell@demo ]; then source_file=named.kmk; fi
		"${runner[@]}" -C "$project" -f "$source_file" "${sample%|*}" >"$project/value" 2>"$project/error"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend capture target ${sample%|*}"; else test-fail "$backend bare capture or exact precedence"; fi
	done
	status=0
	"${runner[@]}" -C "$project" aws-shell@demo >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q TGT_AMBIG "$project/error"; then test-ok "$backend overlapping capture rules remain ambiguous"; else test-fail "$backend capture ambiguity"; fi
done
test-end
