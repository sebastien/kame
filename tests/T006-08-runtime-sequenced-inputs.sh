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
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}

for backend in native wasm; do
	project="$TEST_PATH/$backend"
	mkdir -p "$project"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	cat >"$project/Makefile.km" <<'KAME'
task parallel-a : ; {shell: "sh"}
	while [ ! -e ready ]; do sleep 0.05; done
task parallel-b :
	touch ready
task parallel : parallel-a parallel-b
task bounded-a :
	sleep 0.5
task bounded-b : ; {shell: "sh"}
	sleep 0.5
task bounded : bounded-a bounded-b
rule ./generated :
	printf generated > $outputs.0
task dynamic-job :
	printf '%s' (text (read ./generated)) | cat > ./dynamic-result
task first :
	touch first-done
task second :
	test -e first-done
	touch second-done
task sequenced : first, second
task cached cache-sequence : first, second
	printf c >> cache-sequence-runs
task broken :
	false
task later :
	touch later-ran
task blocked : broken, later
	touch forbidden
PREP = ["first"]
BUILD = ["second"]
task computed : ${PREP}, ${BUILD}
task computed-parallel : ${["parallel-a", "parallel-b"]}
task empty-middle : ${PREP}, ${[]}, ${BUILD}
rule ./membership :
	printf second > $outputs.0
task discovered : ${[./membership]}, ${text (read ./membership)}
task computed-blocked : ${["broken"]}, ${text (read ./must-not-read)}
KAME

	test-step "$backend keeps whitespace dependencies parallel"
	if timeout 30 "${runner[@]}" -j 2 -C "$project" parallel >"$project/out" 2>"$project/err" &&
		[ -e "$project/ready" ]; then test-ok "$backend started independent prerequisites together"; else test-fail "$backend serialized whitespace prerequisites"; fi

	test-step "$backend bounds active jobs across scheduler rounds"
	if timeout 25 "${runner[@]}" --json -j 1 -C "$project" bounded >"$project/jobs" 2>"$project/err" &&
		jq -e -s '[.[] | select(.type == "process-started" or .type == "process-exited") | [.type, .target]] as $events |
			($events | map(.[0])) == ["process-started", "process-exited", "process-started", "process-exited"] and
			$events[0][1] == $events[1][1] and $events[2][1] == $events[3][1] and
			([$events[0][1], $events[2][1]] | sort) == ["bounded-a", "bounded-b"]' "$project/jobs" >/dev/null; then
		test-ok "$backend serialized independent Kash and foreign-shell recipes"
	else
		test-fail "$backend exceeded the single job limit: $(cat "$project/err")"
	fi
	if timeout 25 "${runner[@]}" -j 1 -C "$project" dynamic-job >"$project/out" 2>"$project/err" &&
		[ "$(cat "$project/dynamic-result")" = generated ]; then
		test-ok "$backend yielded to a discovered file producer and ran a pipeline in one slot"
	else
		test-fail "$backend blocked a dynamic prerequisite or pipeline: $(cat "$project/err")"
	fi

	test-step "$backend waits at comma sequencing boundaries"
	"${runner[@]}" -C "$project" sequenced >"$project/out" 2>"$project/err"
	if [ -e "$project/first-done" ] && [ -e "$project/second-done" ]; then test-ok "$backend completed each sequence group in order"; else test-fail "$backend comma groups did not complete in order: $(cat "$project/err")"; fi

	test-step "$backend computed groups and generated membership"
	for target in computed empty-middle discovered; do
		rm -f "$project/first-done" "$project/second-done"
		if [ "$target" = discovered ]; then touch "$project/first-done"; fi
		"${runner[@]}" -C "$project" "$target" >"$project/out" 2>"$project/err"
		if [ -e "$project/second-done" ]; then test-ok "$backend $target completed in order"; else test-fail "$backend $target lost computed members"; fi
	done
	rm -f "$project/ready"
	if timeout 30 "${runner[@]}" -j 2 -C "$project" computed-parallel >"$project/out" 2>"$project/err" &&
		[ -e "$project/ready" ]; then test-ok "$backend computed list members remain parallel"; else test-fail "$backend serialized computed list members"; fi
	"${runner[@]}" do plan --json -C "$project" empty-middle >"$project/empty-plan" 2>"$project/err"
	if jq -e '[.dependencies[] | select(.producer == 1 and .group != null) | .group] == [1, 3]' "$project/empty-plan" >/dev/null; then test-ok "$backend retains authored groups across empty expansion"; else test-fail "$backend lost empty expansion barrier"; fi
	if "${runner[@]}" -C "$project" computed-blocked >"$project/out" 2>"$project/err"; then test-fail "$backend accepted failed computed group";
	elif ! grep -q 'must-not-read' "$project/err"; then test-ok "$backend did not evaluate the later group after failure";
	else test-fail "$backend evaluated a later group before the barrier"; fi

	test-step "$backend cache identity includes sequence boundaries"
	"${runner[@]}" -C "$project" cache-sequence >"$project/out" 2>"$project/err"
	sed -i 's/task cached cache-sequence : first, second/task cached cache-sequence : first second/' "$project/Makefile.km"
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

	"${runner[@]}" do fmt --lang km "$project/Makefile.km" >"$project/formatted" 2>"$project/err"
	"${runner[@]}" do plan --json -C "$project" sequenced >"$project/plan" 2>"$project/err"
	if rg -q '^task sequenced : first, second$' "$project/formatted" && jq -e '[.dependencies[] | select(.producer == 1 and .group != null) | .group] == [1, 2]' "$project/plan" >/dev/null; then test-ok "$backend formatter and plan preserve sequencing boundaries"; else test-fail "$backend sequencing inspection lost a boundary"; fi
done

test-end
