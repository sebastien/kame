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
