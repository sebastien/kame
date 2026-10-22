#!/usr/bin/env bash
# Spec: docs/spec/046-kash-scripting-and-publication.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T046-01 Kash scripting and publication"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	check() {
		local expected="$1" source="$2" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend scripting result"; else
			test-fail "$backend scripting result ($status)"
			cat "$work/out" "$work/err"
		fi
	}
	reject() {
		local code="$1" source="$2" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend rejects $code"; else
			test-fail "$backend expected $code ($status)"
			cat "$work/err"
		fi
	}
	test-step "$backend typed iteration, fresh scopes and ordered effects"
	check '<one><two>' $'for item in ["one" "two"]\n\tprintf "<%s>" $item'
	check 'listab' $'for item in (let [ignored (out! "list")] ["a" "b"])\n\tprintf "%s" $item'
	check 'condition' $'while (let [ignored (out! "condition")] :false)\n\t(nop)'
	check 'outera1a2b1b2outer' $'item = "outer"\nprintf "%s" $item\nfor item in ["a" "b"]\n\tvalue = $(printf "%s" $item)\n\tfor n in [1 2]\n\t\tprintf "%s%s" $value $n\nprintf "%s" $item'
	check 'abBA' $'for item in ["a" "b"]\n\tprintf "%s" $item\n\tdefer printf "%s" (uppercase item)'
	check 'abBA' $'for item in ["a" "b"]\n\tprintf "%s" $item\n\tdefer (out! (uppercase item))'
	check 'done' $'for item in []\n\tmissing-executable\nwhile (nop :false)\n\tmissing-executable\nprintf done'
	check 'localalocalb' $'for item in ["a" "b"]\n\tvalue = (let [ignored (out! "local")] item)\n\tprintf "%s" $value $(printf "")'
	check 'ab' $'script = (kash """for item in args\n\tprintf "%s" $item\n""")\n(script "a" "b")'
	check 'aa' $'for callback in [([x] x) ([x] x)]\n\tprintf "%s" (callback "a")\n\t(nop ([x] x))\n\tif (eq (callback "b") "b")\n\t\t(nop)'
	reject EXPR_INVALID $'for item in "text"\n\tprintf wrong'
	reject EXPR_INVALID $'for item in ([x] x)\n\t(nop)'
	reject REF_MISSING $'for item in [1]\n\t(nop item)\nprintf "%s" $item'
	reject RECIPE_FAIL $'for item in [1 2]\n\tprintf "%s" $item\n\tfalse'
	if [ "$(cat "$work/out")" = 1 ]; then test-ok "$backend loop is fail-fast"; else test-fail "$backend continued after failure"; fi
	cat >"$work/probe" <<'SH'
#!/bin/sh
printf x >> probes
test ! -e finished
SH
	chmod +x "$work/probe"
	check 'body' $'while ./probe\n\tprintf body\n\ttouch finished'
	if [ "$(cat "$work/probes")" = xx ]; then test-ok "$backend while launches a fresh condition"; else test-fail "$backend reused a condition"; fi
	rm "$work/finished" "$work/probes"
	check 'body' $'probe = (kash """./probe ?""")\nwhile (eq (get "status" (probe)) 0)\n\tprintf body\n\ttouch finished'
	test-step "$backend pure option parsing and Unicode token boundaries"
	check 'a b|c|main.css|print.css' $'schema = [fonts: [names: ["-f" "--fonts"] arity: :many repeat: :append] css: [names: ["-c" "--css"] arity: :many repeat: :append]]\nparsed = (parse-args schema ["-f" "a b" "--css" "main.css" "--fonts" "c" "--css" "print.css"])\nprintf "%s" (join "|" (concat parsed.options.fonts parsed.options.css))'
	check 'New,New Font-alt éFont Font² FontⅣ /New/ $1' $'printf "%s" (sub-token "Font" "New" "Font,Font Font-alt éFont Font² FontⅣ /Font/ $1")'
	reject ARGS_INVALID '(parse-args [x: [names: ["-x"] arity: :one repeat: :reject]] ["-x" "--private-sentinel"])'
	if ! grep -q private-sentinel "$work/err"; then test-ok "$backend parser diagnostic redacts values"; else test-fail "$backend parser leaked argv"; fi
	for source in $'for env in []\n\ttrue' $'for x in []\n\tx = 1' $'for x of []\n\ttrue' $'while true; false\n\ttrue' $'while $(true)\n\ttrue' $'for x in []\ntrue'; do reject PARSE_ERR "$source"; done
	test-step "$backend loops retain timeout and dry-run boundaries"
	status=0
	invoke --timeout 50 -l kash -c $'while (nop :true)\n\t(nop)' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q RECIPE_TIMEOUT "$work/err"; then test-ok "$backend pure while yields for timeout"; else test-fail "$backend pure while timeout"; fi
	invoke -n -l kash -c $'while touch forbidden\n\ttouch forbidden\nfor x in $(touch forbidden)\n\ttouch forbidden' >"$work/out" 2>"$work/err"
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend dry-run does not probe loops"; else test-fail "$backend dry-run ran effects"; fi
	test-step "$backend no-clobber publication"
	printf complete >"$work/staged"
	check 'complete' $'(publish! ./staged ./published)\ncat < ./published'
	if [ ! -e "$work/staged" ]; then test-ok "$backend staging consumed"; else test-fail "$backend staging remains"; fi
	printf other >"$work/staged"
	reject FS_EXISTS '(publish! ./staged ./published)'
	if [ "$(cat "$work/published")" = complete ] && [ "$(cat "$work/staged")" = other ]; then test-ok "$backend collision preserves both files"; else test-fail "$backend clobbered a file"; fi
	ln -s missing "$work/dangling"
	reject FS_EXISTS '(publish! ./staged ./dangling)'
	ln -s staged "$work/staged-link"
	reject FS_ERR '(publish! ./staged-link ./unexpected)'
	mkdir "$work/existing-directory"
	reject FS_EXISTS '(publish! ./staged ./existing-directory)'
	reject FS_UNSUPPORTED '(publish! (resource "mem://workspace/staged") ./unexpected)'
	invoke -n -l kash -c '(publish! ./staged ./dry-published)' >"$work/out" 2>"$work/err"
	if [ -f "$work/staged" ] && [ ! -e "$work/dry-published" ]; then test-ok "$backend dry-run does not publish"; else test-fail "$backend dry-run publication"; fi
	if [ -d /dev/shm ] && [ -w /dev/shm ] && [ "$(stat -c %d "$work")" != "$(stat -c %d /dev/shm)" ]; then
		cross=$(mktemp /dev/shm/kame-publication.XXXXXX)
		printf cross >"$cross"
		reject FS_UNSUPPORTED "(publish! \"$cross\" ./cross-published)"
		if [ -f "$cross" ] && [ ! -e "$work/cross-published" ]; then test-ok "$backend cross-filesystem failure preserves staging"; else test-fail "$backend cross-filesystem publication"; fi
		rm "$cross"
	fi
	status=0
	"${runner[@]}" do run -C "$work" --allow-read="$work/staged" --allow-write="$work/published" -l kash -c '(publish! ./staged ./denied)' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ -f "$work/staged" ] && [ ! -e "$work/denied" ]; then test-ok "$backend publication grants checked before mutation"; else test-fail "$backend publication authority"; fi
	if [ "$(id -u)" != 0 ]; then
		mkdir "$work/private"
		printf complete >"$work/private/staged"
		chmod 500 "$work/private"
		status=0
		invoke --json -l kash -c '(publish! ./private/staged ./partial)' >"$work/partial.json" 2>"$work/err" || status=$?
		chmod 700 "$work/private"
		if [ "$status" = 1 ] && [ "$(cat "$work/partial")" = complete ] && [ -f "$work/private/staged" ] && jq -es 'any(.[]; .diagnostic.code == "FS_PUBLISHED" and .diagnostic.cause.published == true and .diagnostic.cause.stagedRemoved == false)' "$work/partial.json" >/dev/null; then test-ok "$backend post-publication failure preserves commit outcome"; else
			test-fail "$backend partial publication reporting"
			cat "$work/partial.json" "$work/err"
		fi
	fi
	test-step "$backend synchronized competing publishers"
	for n in 1 2; do
		printf 'writer-%s' "$n" >"$work/stage-$n"
		mkfifo "$work/ready-$n" "$work/gate-$n"
		invoke -l kash -c "sh -c \"printf ready > ready-$n; cat < gate-$n\"; (publish! ./stage-$n ./winner)" >"$work/race-$n.out" 2>"$work/race-$n.err" &
		if [ "$n" = 1 ]; then pid1=$!; else pid2=$!; fi
	done
	cat "$work/ready-1" >/dev/null
	cat "$work/ready-2" >/dev/null
	printf go >"$work/gate-1" &
	gate1=$!
	printf go >"$work/gate-2" &
	gate2=$!
	status1=0
	status2=0
	wait "$pid1" || status1=$?
	wait "$pid2" || status2=$?
	wait "$gate1" "$gate2"
	if { [ "$status1:$status2" = 0:1 ] || [ "$status1:$status2" = 1:0 ]; } && grep -q FS_EXISTS "$work"/race-*.err && { [ "$(cat "$work/winner")" = writer-1 ] || [ "$(cat "$work/winner")" = writer-2 ]; }; then test-ok "$backend exactly one publisher wins"; else
		test-fail "$backend publication race"
		cat "$work"/race-*.err
	fi
	test-step "$backend loop syntax round trips"
	printf '%s\n' 'for item in [1 2]' '  while (nop :false)' '    printf "%s" $item' >"$work/source.kash"
	"${runner[@]}" do parse -l kash "$work/source.kash" >"$work/ast"
	"${runner[@]}" do fmt -l kash "$work/source.kash" >"$work/formatted"
	"${runner[@]}" do fmt -l kash "$work/formatted" >"$work/again"
	if cmp -s "$work/formatted" "$work/again" && grep -q kash-for "$work/ast" && grep -q kash-while "$work/ast"; then test-ok "$backend loop AST and formatter"; else test-fail "$backend loop round trip"; fi
	cat >"$work/Makefile.kmk" <<'KAME'
SHELL = kash
default :
	for item in ["first" "second"]
		if (nop :true)
			written = (write! (joinpath "." item) item)
			(nop written)
KAME
	invoke "$work/Makefile.kmk" default >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/first")" = first ] && [ "$(cat "$work/second")" = second ]; then test-ok "$backend loop recipe retains deferred writes"; else test-fail "$backend loop recipe lost effects"; fi
done
test-step "embedding publication wire outcomes and callback grants"
if KAME_ROOT="$CLI_ROOT" node --input-type=module - <<'NODE'; then test-ok "publication callback protocol"; else test-fail "publication callback protocol"; fi
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
const root = process.env.KAME_ROOT;
const { Kame } = await import(pathToFileURL(join(root, 'dist/kame.js')));
const kame = await Kame.create({ wasmPath: join(root, 'dist/kame.wasm') });
try {
  const program = await kame.compile('');
  let calls = 0;
  let outcome = { code: '', published: true, stagedRemoved: true };
  const options = {
    cwd: root,
    grants: [{ capability: 'read', names: ['./virtual-stage'] }, { capability: 'write', names: ['./virtual-stage', './virtual-final'] }],
    hostRequest: async request => {
      calls++;
      assert.equal(request.kind, 39);
      assert.equal(request.capability, 'write');
      const payload = JSON.parse(request.payload);
      assert.equal(payload.staged, join(root, 'virtual-stage'));
      assert.equal(payload.destination, join(root, 'virtual-final'));
      return { type: 'json', value: outcome };
    },
  };
  const source = '(publish! "./virtual-stage" "./virtual-final")';
  assert.match(await program.evaluate(source, options), /published: :true/);
  assert.equal(calls, 1);
  await assert.rejects(program.evaluate(source, {
    ...options, grants: [{ capability: 'read' }, { capability: 'write', names: ['./virtual-final'] }],
  }), error => error.code === 'CAP_DENIED');
  assert.equal(calls, 1, 'denied publication reached the callback');
  outcome = { code: 'FS_PUBLISHED', published: true, stagedRemoved: false };
  await assert.rejects(program.evaluate(source, options), error => error.code === 'FS_PUBLISHED');
  outcome = { code: '', published: false, stagedRemoved: false };
  await assert.rejects(program.evaluate(source, options), error => error.code === 'HOST_FAIL');
} finally { await kame.dispose(); }
NODE
test-end
