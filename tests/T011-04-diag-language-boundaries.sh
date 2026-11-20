#!/usr/bin/env bash
# Spec: docs/spec/011-diagnostics.md — language-boundary recovery
# Spec: docs/spec/055-unified-kame-kash-language.md — authored spans across grammar boundaries
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T011-04 language-boundary recovery"
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	reject() {
		local code="$1" guidance="$2"; shift 2
		for format in human plain json; do
			local options=(--color=never --diagnostic-format="$format")
			local report="$work/error" status=0
			if [ "$format" = json ]; then options=(--json); report="$work/output"; fi
			"${runner[@]}" "${options[@]}" "$@" >"$work/output" 2>"$work/error" || status=$?
			local guidance_report="$report"
			if [ "$format" = json ]; then
				jq -r 'select(.diagnostic != null) | .diagnostic | (.message, .tips[]?)' "$report" >"$work/guidance"
				guidance_report="$work/guidance"
			fi
			if [ "$status" = 1 ] && grep -Fq "$code" "$report" && grep -Fq -- "$guidance" "$guidance_report"; then
				test-ok "$backend $format $code recovery"
			else
				test-fail "$backend $format $code recovery ($status)"
				cat "$work/output" "$work/error"
			fi
		done
	}
	test-step "$backend reference versus call recovery"
	reject EXPR_INVALID '${f ()}' do run --lang expr -c '((f => "${f}") (() => "ok"))'
	reject EXPR_INVALID 'not callable' do run --lang expr -c '((name => "${name ()}") "ok")'
	reject PARSE_ERR 'expected' do run --lang expr -c '"${(str "x"}"'
	reject PARSE_ERR 'expected' do run --lang expr -c '"${name'
	reject PARSE_ERR 'unclosed template interpolation' do render --comment plain -c 'prefix @{name'
	reject PARSE_ERR 'unclosed template interpolation' do run --allow-run --lang km -c $'task default ; {shell: "sh"}\n\tprintf "prefix @{name"\n'
	reject PARSE_ERR 'expected' do run --lang kash -c 'printf prefix-(str "unclosed"'
	test-step "$backend parser ownership and authority"
	if [ "$("${runner[@]}" do render --comment plain -c '--mtime=@0 @<')" = '--mtime=@0 @<' ]; then test-ok "$backend renders literal at-signs"; else test-fail "$backend changed literal template text"; fi
	reject PARSE_ERR 'expected expression' do run --lang expr -c '@<'
	reject PARSE_ERR 'expected expression' do run --lang expr -c '@*'
	reject SEL_NO_CONTEXT 'pass the list explicitly' do run --lang expr -c 'inputs.0'
	reject CAP_DENIED '--allow-env=NAME' do run --lang expr -c 'env/MODE'
	reject PARSE_ERR 'unexpected declaration header text' do run --lang km -c 'task show ; {shell: "sh"} :'
	reject PARSE_ERR 'use ${expr}' do run --allow-run --lang kash -c 'printf @{name}'
	reject PARSE_ERR './ for a file path' do run --lang km -c 'rule out.o :'
	test-step "$backend retains call and iteration context across a scoped include"
	cat >"$work/Makefile.km" <<'KAME'
def combine item = missing item
for variant in ["alpha", "beta"]
	include ./loop.km
KAME
	cat >"$work/loop.km" <<'KAME'
for revision in ["one", "two"]
	let flavor = variant
	rule ./${flavor}-${revision}.txt
		printf '%s' prefix-(combine flavor) > $outputs.0
KAME
	status=0
	"${runner[@]}" --json -C "$work" ./beta-one.txt >"$work/nested.jsonl" 2>"$work/nested.err" || status=$?
	if [ "$status" = 1 ] && [ ! -e "$work/beta-one.txt" ] &&
		jq -es --arg root "$work/Makefile.km" --arg loop "$work/loop.km" \
			--rawfile root_text "$work/Makefile.km" --rawfile loop_text "$work/loop.km" '
			def snippet($text): .span as $span | $text[$span.start:$span.end];
			any(.[]; .type == "diagnostic" and (.diagnostic |
				.code == "OP_UNKNOWN" and .source == $root and snippet($root_text) == "missing" and
				any(.frames[]; .kind == "call" and .label == "combine" and .source == $loop and snippet($loop_text) == "combine flavor") and
				([.frames[] | select(.kind == "iteration") |
					[.label, .source, (if .source == $root then snippet($root_text) else snippet($loop_text) end)]] ==
					[["revision #1", $loop, "revision"], ["variant #2", $root, "variant"]])))
		' "$work/nested.jsonl" >/dev/null; then
		test-ok "$backend retains the failure, the argument call site, and both iterations"
	else
		test-fail "$backend nested diagnostic lost authored context (status=$status)"
		cat "$work/nested.jsonl" "$work/nested.err"
	fi

	test-step "$backend generated rule failures and collisions retain construction context"
	for scenario in body collision; do
		variants='"alpha", "beta"'
		code=EXPR_INVALID
		iterations='["variant #2"]'
		if [ "$scenario" = collision ]; then
			variants='"beta", "beta"'
			code=TGT_AMBIG
			iterations='["variant #1", "variant #2"]'
		fi
		cat >"$work/Makefile.km" <<KAME
def make-output name =
	rule ./\${name}.txt = first 7
for variant in [$variants]
	generate (make-output variant)
KAME
		status=0
		"${runner[@]}" --json -C "$work" ./beta.txt >"$work/generated.jsonl" 2>"$work/generated.err" || status=$?
		if [ "$status" = 1 ] && [ ! -e "$work/beta.txt" ] &&
			jq -es --arg code "$code" --arg path "$work/Makefile.km" --argjson iterations "$iterations" \
				--rawfile authored "$work/Makefile.km" '
				def snippet: .span as $span | $authored[$span.start:$span.end];
				any(.[]; .type == "diagnostic" and (.diagnostic |
					.code == $code and .source == $path and
					any(.frames[]; .kind == "call" and .label == "make-output" and .source == $path and snippet == "make-output variant") and
					any(.frames[]; .kind == "generate" and .source == $path and (snippet | contains("generate (make-output variant)"))) and
					([.frames[] | select(.kind == "iteration" and .source == $path and snippet == "variant") | .label] == $iterations)))
				' "$work/generated.jsonl" >/dev/null; then
			test-ok "$backend preserves the constructor, generate site, and iterations for $scenario"
		else
			test-fail "$backend generated $scenario lost authored context (status=$status)"
			cat "$work/generated.jsonl" "$work/generated.err"
		fi
	done
	test-step "$backend quoted literal at-signs retain argument bytes through formatting"
	source='printf "%s" "--mtime=@0"'
	"${runner[@]}" do fmt --lang kash -c "$source" >"$work/at.kash"
	"${runner[@]}" do fmt --lang kash "$work/at.kash" >"$work/at-again.kash"
	"${runner[@]}" do run --allow-run "$work/at.kash" >"$work/at-output"
	if cmp -s "$work/at.kash" "$work/at-again.kash" && [ "$(cat "$work/at-output")" = '--mtime=@0' ]; then test-ok "$backend literal at-sign format round trip"; else test-fail "$backend literal at-sign changed"; fi
	test-step "$backend scalar destinations never reveal evaluated secrets"
	secret='boundary-secret-that-must-not-be-disclosed'
	for setup in '' 'cwd: ' 'timeout: ' 'MODE: '; do
		if [ -z "$setup" ]; then source='printf prefix-(nop {hidden: env/RECOVERY_SECRET})'; else source="${setup}"'prefix-(nop {hidden: env/RECOVERY_SECRET}) printf forbidden'; fi
		RECOVERY_SECRET="$secret" reject EXPR_INVALID 'record' do run --allow-run --allow-env=RECOVERY_SECRET --lang kash -c "$source"
		if grep -Fq "$secret" "$work/output" "$work/error"; then test-fail "$backend evaluated secret in diagnostics"; else test-ok "$backend evaluated secret remains private"; fi
	done
done
test-end
