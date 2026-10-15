#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — scalar expression words and retained fragments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-21 mixed Kash expression words"
test-step "build native and WASM artifacts"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/build/debug dir" "$work/out"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		if "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write --lang kash -c "$1" >"$work/stdout" 2>"$work/stderr" && [ "$(cat "$work/stdout")" = "$expected" ]; then
			test-ok "$backend scalar word"
		else
			test-fail "$backend scalar word"
			cat "$work/stdout" "$work/stderr"
		fi
	}
	test-step "$backend composition, quoting and standalone list splicing"
	check '<--config-path=a b.json><name=a b><a b.json>' $'(name) = "a b"\nprintf "<%s>" --config-path=(name).json name=(name) (name).json'
	check '<prefix-a b-3-true-suffix><{a b}>' $'(name) = "a b"\nprintf "<%s>" prefix-(name)-(nop 3)-(nop :true)-suffix {(name)}'
	check '<--config-path=(missing)><(literal)><[x]>' 'printf "<%s>" "--config-path=(missing)" \(literal\) "[x]"'
	check '<><two words><tail>' $'(modules) = ["" "two words"]\nprintf "<%s>" (modules) tail'
	check '<value><captured><empty->' $'head = "printf"\n${head}(nop "") "<%s>" ("value" | str) (str $(printf captured)) empty-(nop "")'
	check '<quoted suffix>' 'printf "<%s>" (nop "quoted")" suffix"'
	check '<$(touch forbidden) | ; > @{}>' $'data = """$(touch forbidden) | ; > @{}"""\nprintf "<%s>" (nop data)'

	test-step "$backend scalar setup and redirection destinations"
	check 'release-debug' $'(variant) = "debug"\n:MODE release-(variant) printenv MODE'
	check 'cwd' $'(variant) = "debug dir"\n:cwd ./build/(variant) :timeout (nop 1).5 printf cwd'
	check 'artifact' $'(name) = "a b"\nprintf artifact > ./out/(name).txt\ncat < (str "./out/" (name)).txt'
	check 'typed' $'exports = [MODE: "typed"]\n:env $exports printenv MODE'
	check '' $'if (nop :false)\n  printf forbidden-(missing)'

	test-step "$backend invalid scalars fail before launch or truncation"
	for operand in 'prefix-(nop [1])' '(nop :nil).txt' '(nop [secret: "never-print-this"]).txt' '(nop ([x] x)).txt'; do
		for destination in argument head redirect cwd timeout environment; do
			printf unchanged >"$work/protected"
			case "$destination" in
				argument) source="printf forbidden $operand > ./protected"; guidance=argument ;;
				head) source="prefix-$operand > ./protected"; guidance='command head' ;;
				redirect) source="printf forbidden > $operand"; guidance=redirection ;;
				cwd) source=":cwd $operand printf forbidden > ./protected"; guidance='setup :cwd' ;;
				timeout) source=":timeout $operand printf forbidden > ./protected"; guidance='setup :timeout' ;;
				environment) source=":MODE $operand printf forbidden > ./protected"; guidance='setup :MODE' ;;
			esac
			status=0
			"${runner[@]}" do run --json -C "$work" --allow-run --allow-write --lang kash -c "$source" >"$work/error.jsonl" 2>"$work/stderr" || status=$?
			if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/error.jsonl" && grep -q "$guidance" "$work/error.jsonl" && ! grep -q 'process-started' "$work/error.jsonl" && [ "$(cat "$work/protected")" = unchanged ]; then
				test-ok "$backend rejects $destination scalar before launch"
			else
				test-fail "$backend scalar rejection ($destination/$status)"
				cat "$work/error.jsonl" "$work/stderr"
			fi
		done
	done
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend result bytes remain inert"; else test-fail "$backend result bytes executed"; fi

	test-step "$backend format preserves words and expression boundaries"
	printf '%s\n' '(name) = "a b"' 'printf "<%s>" --config=(name).json (name)" suffix" "literal(parentheses)" ("a" | str)' >"$work/source.kash"
	"${runner[@]}" do fmt --lang kash "$work/source.kash" >"$work/formatted.kash"
	"${runner[@]}" do fmt --lang kash "$work/formatted.kash" >"$work/again.kash"
	"${runner[@]}" do run --allow-run "$work/source.kash" >"$work/before"
	"${runner[@]}" do run --allow-run "$work/formatted.kash" >"$work/after"
	if cmp -s "$work/formatted.kash" "$work/again.kash" && cmp -s "$work/before" "$work/after"; then test-ok "$backend mixed word format round trip"; else test-fail "$backend mixed word format changed behavior"; fi
done
test-end
