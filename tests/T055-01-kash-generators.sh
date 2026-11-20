#!/usr/bin/env bash
# Spec: docs/spec/055-unified-kame-kash-language.md — generator functions
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T055-01 Kash generators"
cd "$CLI_ROOT"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi

for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	test-step "$backend yields only as the consumer advances"
	check() {
		local expected="$1" program="$2" status=0
		printf '%s' "$expected" >"$TMPDIR/expected"
		"${runner[@]}" do run --lang kash -c "$program" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
		if [ "$status" = 0 ] && cmp -s "$TMPDIR/out" "$TMPDIR/expected"; then
			test-ok "$backend exact generator output"
		else
			test-fail "$backend unexpected generator result ($status)"
			cat "$TMPDIR/out" "$TMPDIR/err"
		fi
	}

	program=$(cat <<'KASH'
def count-from start
	out! "pull\n"
	for offset in [1, 2]
		if offset > 0
			yield (start + offset)
	yield nil
	return
	printf "unreachable\n"
let pending = count-from 10
let unused = count-from 20
printf "created\n"
for item in pending
	(out! (fmt item) "\n")
KASH
)
	check $'created\npull\n11\n12\nnil\n' "$program"

	program=$(cat <<'KASH'
def make source
	def retained item
		yield (str source item)
	return retained "!"
let stream_value = make "kept"
for value in stream_value
	printf "%s\n" $value
KASH
)
	check $'kept!\n' "$program"

	program=$(cat <<'KASH'
def words ()
	printf "stdout,"
	yield $(printf "first\n")
	yield $(printf "second\n")
for value in words ()
	printf "<%s>" $value
KASH
)
	check $'stdout,<first\n><second\n>' "$program"

	test-step "$backend runs a generator when invoked as a standalone command or capture"
	program=$(cat <<'KASH'
def produce ()
	out! A
	yield 1
	out! B
	yield 2
produce
let captured = $(produce)
let result = $?(produce)
let job = $&(produce)
let finished = $?(wait $job)
out! ":" $captured ":" $result.value ":" $finished.value
(out! ":" (fmt result.state) ":" (fmt finished.state))
KASH
)
	check 'AB:AB:AB:AB::completed::completed' "$program"
	if "${runner[@]}" do run -n --allow-run --lang expr -c '$?(printf unexpected)' >"$TMPDIR/out" 2>"$TMPDIR/err" && [ ! -s "$TMPDIR/out" ]; then
		test-ok "$backend dry-run produces no process outcome"
	else
		test-fail "$backend dry-run produced an outcome or failed"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi

	test-step "$backend explicitly collects all values or closes after a prefix"
	program=$(cat <<'KASH'
def produce label
	out! $label "\n"
	defer printf "cleanup: %s\n" $label
	yield 7
	yield nil
	yield (line $(printf "last\n"))
let batch = produce "all" |> collect!
(out! (fmt batch) "\n")
let prefix = produce "prefix" |> collect! 2
(out! (fmt prefix) "\n")
let empty = produce "never started" |> collect! 0
(out! (fmt empty) "\n")
KASH
)
	check $'all\ncleanup: all\n[7, nil, "last"]\nprefix\ncleanup: prefix\n[7, nil]\n[]\n' "$program"

	test-step "$backend unwinds deferred cleanup after a consumer stops"
	program=$(cat <<'KASH'
def produce ()
	defer printf "outer\n"
	with cwd: .
		defer printf "inner\n"
		yield "first"
		printf "unreachable\n"
		yield "second"
for item in produce ()
	printf "%s\n" $item
	break
printf "done\n"
KASH
)
	check $'first\ninner\nouter\ndone\n' "$program"

	program=$(cat <<'KASH'
def inner ()
	defer printf "inner\n"
	yield "first"
	yield "unreachable"
def outer ()
	defer printf "outer\n"
	for item in inner ()
		yield item
for item in outer ()
	printf "%s\n" $item
	break
printf "done\n"
KASH
)
	check $'first\ninner\nouter\ndone\n' "$program"

	program=$(cat <<'KASH'
def produce ()
	defer printf "cleanup,"
	yield "prefix"
	yield "unreachable"
def consume ()
	for item in produce ()
		return (str item "-returned")
(out! (consume ()))
KASH
)
	check 'cleanup,prefix-returned' "$program"

	program=$(cat <<'KASH'
def produce ()
	defer printf "cleanup"
	yield 1
	printf "unreachable"
for item in produce ()
	(fail! "consumer failed")
KASH
)
	status=0
	"${runner[@]}" do run --lang kash -c "$program" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'USER_ERROR.*consumer failed' "$TMPDIR/err" &&
		[ "$(cat "$TMPDIR/out")" = cleanup ]; then
		test-ok "$backend producer cleanup runs after consumer failure"
	else
		test-fail "$backend lost producer cleanup or the consumer diagnostic"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi
	program=$(cat <<'KASH'
def produce ()
	defer printf "cleanup"
	yield 1
	(fail! "generator failed")
let batch = produce () |> collect!
printf "unreachable"
KASH
)
	status=0
	"${runner[@]}" do run --lang kash -c "$program" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'USER_ERROR.*generator failed' "$TMPDIR/err" &&
		[ "$(cat "$TMPDIR/out")" = cleanup ]; then
		test-ok "$backend collection preserves generator failure and cleanup"
	else
		test-fail "$backend returned a partial batch or lost generator cleanup"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi

	test-step "$backend rejects a second consumer"
	program=$(cat <<'KASH'
def produce ()
	yield "first"
let values = produce ()
for item in values
	printf "%s" $item
for item in values
	printf "unreachable"
KASH
)
	status=0
	"${runner[@]}" do run --lang kash -c "$program" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'EXPR_INVALID.*stream already has a consumer' "$TMPDIR/err" &&
		[ "$(cat "$TMPDIR/out")" = first ]; then
		test-ok "$backend delivers each value to one consumer"
	else
		test-fail "$backend accepted a second consumer"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi
	program=$(cat <<'KASH'
def produce ()
	yield "unreachable"
let values = produce ()
let empty = collect! 0 values
let invalid = collect! values
KASH
)
	status=0
	"${runner[@]}" do run --lang kash -c "$program" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'EXPR_INVALID.*stream already has a consumer' "$TMPDIR/err" &&
		[ ! -s "$TMPDIR/out" ]; then
		test-ok "$backend an empty prefix still claims its source"
	else
		test-fail "$backend reused a closed stream"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi

	test-step "$backend replays each generator effect once in a recipe"
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	cat >"$work/Kamefile" <<'KAME'
rule ./artifact
	def produce ()
		out! "before first\n"
		yield "first"
		out! "before second\n"
		yield "second"
	for item in produce ()
		(out! (str item "\n"))
	return "artifact\n"

rule ./collected
	def produce ()
		out! "before first\n"
		defer printf "cleanup\n"
		yield "first"
		out! "unreachable\n"
		yield "second"
	let batch = produce () |> collect! 1
	(out! (fmt batch) "\n")
	return "collected\n"

task timeout-production
	def produce ()
		defer printf "cleaned\n"
		out! "started\n"
		yield "first"
		sleep 10
		yield "unreachable"
	let batch = produce () |> collect!
	out! "unreachable\n"
KAME
	printf 'before first\nfirst\nbefore second\nsecond\n' >"$TMPDIR/expected"
	if "${runner[@]}" -C "$work" ./artifact >"$TMPDIR/out" 2>"$TMPDIR/err" &&
		cmp -s "$TMPDIR/out" "$TMPDIR/expected" && [ "$(cat "$work/artifact")" = artifact ]; then
		test-ok "$backend preserves effect order through pull suspensions"
	else
		test-fail "$backend repeated or lost generator effects"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi
	printf 'before first\ncleanup\n["first"]\n' >"$TMPDIR/expected"
	if "${runner[@]}" -C "$work" ./collected >"$TMPDIR/out" 2>"$TMPDIR/err" &&
		cmp -s "$TMPDIR/out" "$TMPDIR/expected" && [ "$(cat "$work/collected")" = collected ]; then
		test-ok "$backend preserves effect order through prefix collection"
	else
		test-fail "$backend repeated or lost prefix collection effects"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi
	test-step "$backend timed-out collection unwinds its producer"
	status=0
	timeout -k 2 12 "${runner[@]}" -C "$work" --timeout 1000 timeout-production >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	printf 'started\ncleaned\n' >"$TMPDIR/expected"
	if [ "$status" = 1 ] && grep -q RECIPE_TIMEOUT "$TMPDIR/err" &&
		cmp -s "$TMPDIR/out" "$TMPDIR/expected"; then
		test-ok "$backend timed-out collection runs cleanup once"
	else
		test-fail "$backend timeout lost cleanup or completed the source"
		cat "$TMPDIR/out" "$TMPDIR/err"
	fi
done
test-end
