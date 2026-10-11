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
	@(out name)
exact :
	@(out "literal")
aws-shell@{role-account} :
	@(out role-account)
KMK
printf 'aws-shell@{role-account} :\n\t@(out role-account)\n' >"$project/named.kmk"
mkdir -p "$project/src/txto/blog/2026/deep"
touch "$project/src/txto/blog/foo.txto" "$project/src/txto/blog/2026/deep/foo.txto" "$project/src/txto/blog/skip.txto"
cat >"$project/replace.kmk" <<'KMK'
SOURCES_TXTO = (wildcard ./src/txto/**/*.txto)
FILTERED = (filter-out ([x] (includes? x "skip")) SOURCES_TXTO)
BUILD_JSON = (replace ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json FILTERED)
SECTION = (replace ./src/txto/{prefix:**}/{name:*}.txto ./build/data/{prefix}/{name}.json)
./result : @(SOURCES_TXTO)
	@(yield (join BUILD_JSON "\n"))
KMK
cp "$project/replace.kmk" "$project/replace.original"
for backend in native wasm; do
	test-step "$backend leading expression patterns and scalar expansions"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
