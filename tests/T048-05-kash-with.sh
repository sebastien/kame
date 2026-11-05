#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — owned execution scopes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-05 Kash owned with scopes"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/a/sub" "$work/b"
	printf R >"$work/value"
	printf A >"$work/a/value"
	printf B >"$work/b/value"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { KAME_WITH_MODE=outer KAME_WITH_KEEP=inherited "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	run_case() {
		local expected="$1" output="$2" text="$3" status=0
		invoke -l kash -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = "$expected" ] && [ "$(cat "$work/out")" = "$output" ]; then test-ok "$backend with status $expected preserves context and ordering";
		else test-fail "$backend with expected $expected ($status)"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1" text="$2" status=0
		invoke -l kash -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code before body effects";
		else test-fail "$backend expected $code ($status)"; cat "$work/out" "$work/err"; fi
	}
	cat >"$work/context.kash" <<'KASH'
exports = [KAME_WITH_MODE: "scope"]
nested = [KAME_WITH_MODE: "nested"]
defn show
	pwd
	printenv KAME_WITH_MODE
with :cwd ./a :env $exports
	(show)
	:cwd ./sub pwd
	with :env $nested :cwd ../b
		(show)
		printenv KAME_WITH_KEEP
	(show)
(show)
KASH
	invoke ./context.kash >"$work/out" 2>"$work/err"
	printf '%s\nscope\n%s\n%s\nnested\ninherited\n%s\nscope\n%s\nouter\n' "$work/a" "$work/a/sub" "$work/b" "$work/a" "$work" >"$work/expected"
	if cmp -s "$work/out" "$work/expected" && [ ! -s "$work/err" ]; then test-ok "$backend nested context, inherited environment, stage override and caller restoration";
	else test-fail "$backend with context"; cat "$work/out" "$work/err"; fi
	run_case 0 ABC $'with :cwd (let [side (out! "A")] "./a") :env (let [side (out! "B")] [KAME_WITH_MODE: "scope"])\n\tprintf C'
	run_case 0 "$work/a"$'\n'"$work" $'with :cwd ./a :env (let [here (line $(pwd))] [BASE: here])\n\tpwd\n\tprintenv BASE'
	run_case 0 ARB $'defn show\n\t(out! (read ./value))\nwith :cwd ./a\n\t(show)\n(show)\nwith :cwd ./b\n\t(show)'
	run_case 0 "$work"$'\n'"$work" $'cached = (line $(pwd))\nprintf "%s\\n" $cached\nwith :cwd ./a\n\tprintf "%s" $cached'
	run_case 0 B1D1X1B2D2X2 $'for item in [1 2]\n\twith\n\t\tlet local = item\n\t\tdefer printf D%s $local\n\t\tprintf B%s $local\n\tprintf X%s $item'
	run_case 0 B21X $'with\n\tfor item in [1 2]\n\t\tif (eq? item item)\n\t\t\tdefer printf %s $item\n\tprintf B\nprintf X'
	run_case 0 BJDX $'with\n\tdefer printf D\n\tsh -c "sleep 0.05; printf J" &\n\tprintf B\nprintf X'
	run_case 0 JDX $'defn probe\n\twith\n\t\tdefer printf D\n\t\tsh -c "sleep 0.02; printf J" &\n\t\treturn 7 ?\n\tprintf forbidden\n(probe)\nprintf X'
	run_case 0 DX $'defn probe\n\twith\n\t\tdefer printf D\n\t\tsh -c "sleep 1; printf forbidden" &\n\t\treturn 7\n\tprintf forbidden\n(probe) ?? "recovered"\nprintf X'
	run_case 0 X $'defn probe\n\twith\n\t\tdefer false\n\t\treturn 0\n\tprintf forbidden\n(probe) ?? "recovered"\nprintf X'
	run_case 23 innerouter $'defer printf outer\nwith\n\tdefer printf inner\n\tsh -c "sleep 1; printf forbidden" &\n\texit 23\nprintf forbidden'
	run_case 23 innerouter $'defer printf outer\nwith\n\tdefer printf inner\n\tsh -c "sleep 1; printf forbidden" &\n\tsleep 0.02\n\texit 23\nprintf forbidden'
	run_case 1 '' $'with\n\tdefer false\n\texit 0\nprintf forbidden'
	run_case 0 R $'let outer = (run! :async :true "sh" "-c" "sleep 0.05; printf R")\ndefn probe\n\twith\n\t\tfalse\n(probe) ?? "recovered"\n(await outer)'
	reject REF_MISSING $'with\n\tlet hidden = "scope"\nprintf %s $hidden'
	reject EXPR_INVALID $'with :cwd ""\n\tprintf forbidden'
	reject EXPR_INVALID $'with :cwd (list "a" "b")\n\tprintf forbidden'
	reject EXPR_INVALID $'with :env "text"\n\tprintf forbidden'
	reject EXPR_INVALID $'exports = [MODE: 1]\nwith :env $exports\n\tprintf forbidden'
	reject EXPR_INVALID $'exports = [BAD-NAME: "value"]\nwith :env $exports\n\tprintf forbidden'
	reject CAP_DENIED $'exports = [MODE: "value"]\nwith :env $exports\n\t(out! (env "MODE"))'
	for header in 'with' 'with :cwd' 'with :env' 'with :timeout 1' 'with :cwd ./a :cwd ./b' 'with :env $one :env $two' 'with :cwd ./a ?? ./b' 'with :cwd ./a &' 'with :cwd ./a > ./executed'; do
		if [ "$header" = with ]; then reject PARSE_ERR "$header"; else reject PARSE_ERR "printf forbidden"$'\n'"$header"$'\n\tprintf forbidden'; fi
	done
	invoke -n -l kash -c $'with :cwd $(touch ./executed) :env (read ./absent)\n\tprintf forbidden\n\tdefer touch ./executed' >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/executed" ]; then test-ok "$backend dry-run acquires no with scope or setup work"; else test-fail "$backend with dry-run effects"; fi
	printf 'with :cwd ./a\n\tdefer (out! (source-dir))\n' >"$work/source.kash"
	invoke ./source.kash >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = "$work" ]; then test-ok "$backend with cwd does not alter authored source identity"; else test-fail "$backend with source identity"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do fmt -l kash "$work/context.kash" >"$work/formatted.kash"
	invoke ./formatted.kash >"$work/out" 2>"$work/err"
	if cmp -s "$work/out" "$work/expected"; then test-ok "$backend with formatting roundtrip"; else test-fail "$backend with formatting"; fi
	"${runner[@]}" do parse --json -l kash "$work/context.kash" >"$work/ast"
	if grep -q '"kind":"kash-with"' "$work/ast" && grep -q '"kind":"command-setup"' "$work/ast"; then test-ok "$backend AST preserves owned scopes and shared setup grammar"; else test-fail "$backend with AST"; fi
done
test-end
