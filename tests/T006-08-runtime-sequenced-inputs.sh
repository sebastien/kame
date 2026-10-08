#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Rules
# Spec: docs/spec/006-runtime.md — Scheduling
# Spec: docs/spec/013-tests.md — T006-08-runtime-sequenced-inputs
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-08 sequenced prerequisites"
test-step "toolchain and shared WASM module"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

for backend in native wasm; do
	project="$TEST_PATH/$backend"
	mkdir -p "$project"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	cat >"$project/Makefile.kmk" <<'KMK'
parallel-a :
	i=0; while [ ! -e ready ] && [ "$i" -lt 2000000 ]; do i=$((i+1)); done
	test -e ready
parallel-b :
	touch ready
parallel : parallel-a parallel-b
first :
	touch first-done
second :
	test -e first-done
	touch second-done
sequenced : first, second
task cache-sequence : first, second
	printf c >> cache-sequence-runs
broken :
	false
later :
	touch later-ran
blocked : broken, later
	touch forbidden
KMK

	test-step "$backend keeps whitespace dependencies parallel"
	"${runner[@]}" -C "$project" parallel >"$project/out" 2>"$project/err"
	if [ -e "$project/ready" ]; then test-ok "$backend started independent prerequisites together"; else test-fail "$backend serialized whitespace prerequisites"; fi

	test-step "$backend waits at comma sequencing boundaries"
	"${runner[@]}" -C "$project" sequenced >"$project/out" 2>"$project/err"
	if [ -e "$project/first-done" ] && [ -e "$project/second-done" ]; then test-ok "$backend completed each sequence group in order"; else test-fail "$backend comma groups did not complete in order: $(cat "$project/err")"; fi

	test-step "$backend cache identity includes sequence boundaries"
	"${runner[@]}" -C "$project" cache-sequence >"$project/out" 2>"$project/err"
	sed -i 's/task cache-sequence : first, second/task cache-sequence : first second/' "$project/Makefile.kmk"
	"${runner[@]}" -C "$project" cache-sequence >"$project/out" 2>"$project/err"
	if [ "$(cat "$project/cache-sequence-runs")" = cc ]; then test-ok "$backend invalidated cache when a sequence boundary changed"; else test-fail "$backend reused a task result across sequence semantics"; fi

	test-step "$backend does not request later groups after failure"
	if "${runner[@]}" -C "$project" blocked >"$project/out" 2>"$project/err"; then
		test-fail "$backend accepted a failed sequence group"
	elif [ ! -e "$project/later-ran" ] && [ ! -e "$project/forbidden" ]; then
		test-ok "$backend propagated failure and left later groups unrequested"
	else
		test-fail "$backend ran a later group after failure"
	fi

	"${runner[@]}" do fmt --lang script "$project/Makefile.kmk" >"$project/formatted" 2>"$project/err"
	"${runner[@]}" do plan --json -C "$project" sequenced >"$project/plan" 2>"$project/err"
	if rg -q '^sequenced : first, second$' "$project/formatted" && jq -e '[.dependencies[] | select(.producer == 1 and .group != null) | .group] == [1, 2]' "$project/plan" >/dev/null; then test-ok "$backend formatter and plan preserve sequencing boundaries"; else test-fail "$backend sequencing inspection lost a boundary"; fi
done

test-end
