#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — lexical loop transfers
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-06 Kash break and continue"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/a"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	run_case() {
		local expected="$1" output="$2" text="$3" status=0
		invoke -l kash -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = "$expected" ] && [ "$(cat "$work/out")" = "$output" ]; then test-ok "$backend transfer status $expected preserves target and ordering";
		else test-fail "$backend expected $expected ($status)"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local text="$1" status=0
		invoke -l kash -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q PARSE_ERR "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects invalid/nonlocal loop transfer before effects";
		else test-fail "$backend expected PARSE_ERR ($status)"; cat "$work/out" "$work/err"; fi
	}
	run_case 0 1X $'for item in [1 2 3]\n\tprintf %s $item\n\tbreak\n\tprintf forbidden\nprintf X'
	run_case 0 123X $'for item in [1 2 3]\n\tlet local = item\n\tprintf %s $local\n\tcontinue; printf forbidden\nprintf X'
	run_case 0 13X $'for item in [1 2 3 4]\n\tif (eq? item 2)\n\t\tcontinue\n\tif (eq? item 4)\n\t\tbreak\n\tprintf %s $item\nprintf X'
	run_case 0 1bX2bXD $'for item in [1 2]\n\tfor inner in ["a" "b"]\n\t\tif (eq? inner "a")\n\t\t\tcontinue\n\t\tprintf %s%s $item $inner\n\t\tbreak\n\tprintf X\nprintf D'
	run_case 0 X $'while (eq? 1 1)\n\tbreak\n\tprintf forbidden\nprintf X'
	run_case 0 X $'touch ./ready\nwhile test -e ./ready\n\trm ./ready\n\tcontinue\n\tprintf forbidden\nprintf X'
	run_case 0 B1D1B2D2X $'for item in [1 2]\n\twith\n\t\tlet local = item\n\t\tdefer printf D%s $local\n\t\tprintf B%s $local\n\t\tcontinue\n\t\tprintf forbidden\n\tprintf forbidden\nprintf X'
	run_case 0 JDX $'for item in [1 2]\n\twith\n\t\tdefer printf D\n\t\tsh -c "sleep 0.02; printf J" &\n\t\tbreak\n\tprintf forbidden\nprintf X'
	run_case 0 JDJDX $'for item in [1 2]\n\twith\n\t\tdefer printf D\n\t\tsh -c "sleep 0.02; printf J" &\n\t\tcontinue\n\tprintf forbidden\nprintf X'
	run_case 0 B21X $'with\n\tfor item in [1 2 3]\n\t\tdefer printf %s $item\n\t\tif (eq? item 2)\n\t\t\tbreak\n\tprintf B\nprintf X'
	run_case 0 B321X $'with\n\tfor item in [1 2 3]\n\t\tdefer printf %s $item\n\t\tcontinue\n\tprintf B\nprintf X'
	run_case 0 X21 $'for item in [1 2 3]\n\tdefer printf %s $item\n\tif (eq? item 2)\n\t\tbreak\nprintf X'
	run_case 0 JX $'let job = (run! :async :true "sh" "-c" "sleep 0.02; printf J")\nfor item in [1]\n\tbreak\n(await job)\nprintf X'
	run_case 1 X $'for item in [1]\n\tfalse &\n\tbreak\nprintf X'
	for transfer in break continue; do
		run_case 0 X "def probe"$'\n\tfor item in [1 2]\n\t\twith\n\t\t\tdefer false\n\t\t\t'"$transfer"$'\n\t\tprintf forbidden\n\tprintf forbidden\n(probe) ?? "recovered"\nprintf X'
		run_case 0 X "def probe"$'\n\tfor item in [1 2]\n\t\twith\n\t\t\tfalse &\n\t\t\t'"$transfer"$'\n\t\tprintf forbidden\n\tprintf forbidden\n(probe) ?? "recovered"\nprintf X'
	done
	run_case 0 12X $'for item in [1 2]\n\tdef helper\n\t\tfor inner in [1 2]\n\t\t\tbreak\n\t\treturn 0\n\t(helper)\n\tprintf %s $item\nprintf X'
	run_case 0 B1D $'def cleanup\n\tfor item in [1 2]\n\t\tprintf %s $item\n\t\tbreak\ndef probe\n\tdefer printf D\n\tdefer (cleanup)\n\tprintf B\n\treturn 0\n(probe)'
	run_case 0 12X $'def cleanup\n\tfor item in [1 2]\n\t\tprintf %s $item\n\t\tcontinue\n(cleanup)\nprintf X'
	run_case 0 breakX $'for item in [1]\n\tprintf %s break\n\tbreak\nprintf X'
	run_case 0 "$work/a"$'\n'"$work/a"$'\n'"$work" $'for item in [1 2]\n\twith :cwd ./a\n\t\tpwd\n\t\tcontinue\npwd'
	for text in 'break' 'continue' $'if (eq? 1 2)\n\tbreak' $'def unused\n\tcontinue' $'for item in [1]\n\tdef unused\n\t\tbreak\n\tprintf forbidden' $'with\n\tcontinue'; do reject "$text"; done
	for transfer in break continue; do
		for suffix in ' 2' ' label' ' ?' ' ?? 0' ' | cat' ' > ./executed' ' &'; do reject "printf forbidden"$'\nfor item in [1]\n\t'"$transfer$suffix"; done
		reject "for item in [1]"$'\n\tdefer '"$transfer"
		reject "runner = (kash \"$transfer\")"$'\nfor item in [1]\n\t(runner)'
	done
	status=0
	# Allow the Go WASM reactor to resolve and arm the defer before testing loop cancellation.
	invoke --timeout 500 -l kash -c $'defer printf D\nwhile (eq? 1 1)\n\tcontinue' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && [ "$(cat "$work/out")" = D ] && grep -q RECIPE_TIMEOUT "$work/err"; then test-ok "$backend continue loop obeys deadlines and finalizes"; else test-fail "$backend continue deadline ($status)"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do run -C "$work" -l kash -c $'for item in [1]\n\tbreak' >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -s "$work/err" ]; then test-ok "$backend standalone transfer launches no command"; else test-fail "$backend standalone transfer"; cat "$work/err"; fi
	invoke -n -l kash -c $'for item in [1]\n\twith :cwd (read ./absent)\n\t\tdefer touch ./executed\n\t\tcontinue' >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/executed" ]; then test-ok "$backend dry-run executes no transfer body/setup/cleanup"; else test-fail "$backend transfer dry-run effects"; fi
	printf 'for item in [1 2]\n\twith\n\t\tif (eq? item 1)\n\t\t\tcontinue\n\t\tprintf %%s $item\n\t\tbreak\n' >"$work/source.kash"
	"${runner[@]}" do fmt -l kash "$work/source.kash" >"$work/formatted.kash"
	invoke ./formatted.kash >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = 2 ]; then test-ok "$backend transfer formatting roundtrip"; else test-fail "$backend transfer formatting"; fi
	"${runner[@]}" do parse --json -l kash "$work/source.kash" >"$work/ast"
	if grep -q '"kind":"kash-break"' "$work/ast" && grep -q '"kind":"kash-continue"' "$work/ast"; then test-ok "$backend AST preserves lexical transfers"; else test-fail "$backend transfer AST"; fi
done
test-end
