#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — Slice A process topology
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T042-01 Kash streaming topology parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/topology"
mkdir -p "$work"

run_case() {
	local text="$1" expected="$2" code="${3:-}" language="${4:-expr}"
	set +e
	timeout 15 "${command[@]}" 'do' run --lang "$language" --allow-run --allow-read --allow-write --capture-limit=32 -c "$text" >"$work/$backend.out" 2>"$work/$backend.err"
	local status=$?
	set -e
	if [ "$status" = "$expected" ]; then test-ok "$backend: $text"; else test-fail "$backend expected $expected got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "$code" ]; then
		if grep -q "$code" "$work/$backend.err"; then test-ok "$backend: $code"; else test-fail "$backend missing $code: $(cat "$work/$backend.err")"; fi
	fi
}

expect() {
	local message="$1"
	shift
	if "$@"; then test-ok "$backend: $message"; else test-fail "$backend: $message"; fi
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend: nested merges and EOF"
	run_case '$((printf "a\n", sh -c "sleep 0.1; echo b") |(merge) wc -l)' 0
	expect "merge waits for both EOFs" grep -Fq '"2\n"' "$work/$backend.out"
	run_case '$((printf a, (printf b, printf c) |(merge) cat) |(merge) wc -c)' 0
	expect "nested merge preserves all bytes" grep -Fq '"3\n"' "$work/$backend.out"
	run_case '$((head -c 4194304 /dev/zero, head -c 4194304 /dev/zero) |(merge) wc -c)' 0
	expect "intermediate streams exceed capture bounds" grep -Fq '"8388608\n"' "$work/$backend.out"
	run_case '$(printf abc |(broadcast) (cat, cat) |(merge) wc -c)' 0
	expect "fan-out and fan-in compose" grep -Fq '"6\n"' "$work/$backend.out"

	test-step "$backend: bounded lossless broadcast with a slow consumer"
	left="$work/$backend-left"
	right="$work/$backend-right"
	run_case "head -c 4194304 /dev/zero |(broadcast) (cat > $left, sh -c \"sleep 0.1; cat\" > $right)" 0 '' kash
	if cmp -s "$left" "$right" && [ "$(wc -c <"$left")" -eq 4194304 ]; then test-ok "$backend lossless fan-out"; else test-fail "$backend lost broadcast bytes"; fi
	run_case "\$(printf abc |(broadcast) (cat > $left, wc -c))" 0
	expect "one designated capture" grep -Fq '"3\n"' "$work/$backend.out"
	expect "independent redirected branch" test "$(cat "$left")" = abc

	test-step "$backend: validation has no launch or truncation effects"
	marker="$work/$backend-marker"
	printf preserve >"$left"
	run_case "\$((sh -c \"touch '$marker'\", printf b) | cat > $left)" 1 KASH_MUX_ARITY
	expect "invalid topology launches nothing" test ! -e "$marker"
	expect "invalid topology preserves output" test "$(cat "$left")" = preserve
	run_case '$(printf a | (cat, cat))' 1 KASH_MUX_ARITY
	run_case '$(printf a |(merge) cat)' 1 KASH_MUX_ARITY
	run_case '$(printf a |(broadcast) cat)' 1 KASH_MUX_ARITY
	run_case '$(printf a |(broadcast) (cat, cat))' 1 KASH_AMBIGUOUS_CAPTURE
	run_case "\$((sh -c \"touch '$marker'\", no-such-kash-tool) |(merge) cat > $left)" 1 TOOL_MISSING
	expect "missing tool launches no peer" test ! -e "$marker"
	expect "missing tool preserves output" test "$(cat "$left")" = preserve
	run_case "\$((cat < $work/missing-input, printf a) |(merge) cat > $left)" 1
	expect "missing input preserves output" test "$(cat "$left")" = preserve
	run_case "printf a |(broadcast) (cat > $left, cat > $work/missing-directory/out)" 1 HOST_FAIL kash
	expect "bad later sink preserves earlier output" test "$(cat "$left")" = preserve

	test-step "$backend: one owned async graph and acceptance"
	run_case "printf abc |(broadcast) (cat > $left, cat > $right) &" 0 '' kash
	expect "async graph joined its first branch" test "$(cat "$left")" = abc
	expect "async graph joined both branches" cmp -s "$left" "$right"
	run_case '$((sh -c "exit 7", sleep 30) |(merge) cat)' 1 RECIPE_FAIL
	run_case '$((sh -c "exit 7", sleep 30) |(merge) cat ?)' 0
	run_case '$((sh -c "exit 7", sleep 30) |(merge) cat ? printf recovered)' 0
	expect "recovery owns the graph" grep -Fq '"recovered"' "$work/$backend.out"
	run_case '$((yes, yes) |(merge) head -c 1)' 1 RECIPE_FAIL
	run_case '$((yes, yes) |(merge) cat)' 1 CAPTURE_LIMIT

	test-step "$backend: timeout reaps source, peer, and native operators"
	pid="$work/$backend.pid"
	pid_token='\$\$'
	status=0
	timeout 10 "${command[@]}" 'do' run --lang kash --allow-run --timeout=200 -c "(sh -c \"echo $pid_token > '$pid'; exec sleep 30\", sleep 30) |(merge) cat" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" -eq 1 ] && grep -q RECIPE_TIMEOUT "$work/$backend.err" && { [ ! -e "$pid" ] || ! kill -0 "$(cat "$pid")" 2>/dev/null; }; then test-ok "$backend timeout reaped graph"; else test-fail "$backend timeout cleanup failed"; fi

	test-step "$backend: discovered generated inputs still gate graph launch and reuse"
	build="$work/$backend-build"
	mkdir -p "$build"
	printf 'seed\n' >"$build/seed"
	cat >"$build/Makefile.kmk" <<'KMK'
SHELL = kash
./source : ./seed
	cat < ./seed > @>
./combined :
	(sh -c "echo run >> runs; cat" < ./source, printf "extra\n") |(merge) sort > @>
./linear :
	sh -c "echo run >> linear-runs; cat" < ./source > @>
KMK
	for attempt in first cached changed; do
		if [ "$attempt" = changed ]; then printf 'changed\n' >"$build/seed"; fi
		if timeout 15 "${command[@]}" -C "$build" -f ./Makefile.kmk ./combined ./linear >"$work/$backend.out" 2>"$work/$backend.err"; then test-ok "$backend discovered prerequisite: $attempt"; else test-fail "$backend generated dependency failed: $(cat "$work/$backend.err")"; fi
		if [ "$attempt" = changed ]; then expected=2; else expected=1; fi
		if [ "$(wc -l <"$build/runs")" -eq "$expected" ]; then test-ok "$backend reuse count: $attempt"; else test-fail "$backend unexpected execution count for $attempt: $(cat "$build/runs")"; fi
		if [ "$(wc -l <"$build/linear-runs")" -eq "$expected" ]; then test-ok "$backend linear reuse count: $attempt"; else test-fail "$backend unexpected linear execution count for $attempt"; fi
	done
	expect "graph consumes changed generated input" test "$(cat "$build/combined")" = $'changed\nextra'
	expect "linear pipeline consumes changed generated input" test "$(cat "$build/linear")" = changed
done

test-end
