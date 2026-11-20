#!/usr/bin/env bash
# Spec: docs/spec/024-managed-services.md — service lifecycle event parity
# Spec: docs/spec/013-tests.md — T010-31-wasm-service-events
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-31 service lifecycle event parity"
cd "$CLI_ROOT"

test-step "build native and wasm CLIs"
if [ -n "${KAME_WASM:-}" ]; then
	wasm_cli="$CLI_ROOT/src/js/kame.js"
else
	make dist-wasm >/dev/null
	wasm_cli="$CLI_ROOT/dist/kame.js"
fi
cli_build

work="$TMPDIR/service-events"
mkdir -p "$work"
cat >"$work/Makefile.km" <<'EOF_MAKE'
service daemon {marker=ready-marker} ; {
	shell: "sh",
	ready: {argv: ["test", "-f", marker], interval-ms: 10, timeout-ms: 3000},
	health: {argv: ["true"], interval-ms: 50, failures: 2},
	restart: {attempts: 1, backoff-ms: 10},
	stop: {grace-ms: 0},
}
	if [ ! -f first-start ]; then touch first-start; exit 1; fi; touch @{marker}; while :; do sleep 1; done
task consumer : daemon
	sleep 0.2
EOF_MAKE

invalid="$work/invalid"
mkdir -p "$invalid"
cat >"$invalid/Makefile.km" <<'EOF_MAKE'
task prepare :
	touch forbidden-prepare
service daemon : prepare ; {ready: {argv: ["true"], interval-ms: true}}
	touch forbidden-service
task default : daemon
EOF_MAKE

for host in native wasm; do
	if [ "$host" = native ]; then
		cli=("$CLI_BIN")
	else
		cli=(node "$wasm_cli")
	fi
	(cd "$work" && "${cli[@]}" do plan --json daemon marker=alternate-ready) >"$work/$host.plan"
	if jq -e 'any(.producers[]; .arguments.marker == "alternate-ready")' "$work/$host.plan" >/dev/null && [ ! -e "$work/alternate-ready" ]; then
		test-ok "$host exposes the service argument override without starting the service"
	else
		test-fail "$host service argument planning"
	fi
	rm -f "$work/first-start" "$work/ready-marker"
	(cd "$work" && timeout 10 "${cli[@]}" --json consumer) >"$work/$host.jsonl" 2>"$work/$host.json.err"
	rm -f "$work/first-start" "$work/ready-marker"
	(cd "$work" && timeout 10 "${cli[@]}" consumer) >"$work/$host.out" 2>"$work/$host.human.err"
	status=0
	(cd "$invalid" && timeout 10 "${cli[@]}" --json default) >"$work/$host.invalid.jsonl" 2>"$work/$host.invalid.err" || status=$?
	if [ "$status" = 1 ] && [ ! -e "$invalid/forbidden-prepare" ] && [ ! -e "$invalid/forbidden-service" ] &&
		jq -es --rawfile source "$invalid/Makefile.km" '
			all(.[]; .type != "process-started") and
			any(.[]; .type == "diagnostic" and (.diagnostic | .code == "EXPR_INVALID" and
				(.source | endswith("Makefile.km")) and
				(.span as $span | $source[$span.start:$span.end] | contains("interval-ms: true"))))
		' "$work/$host.invalid.jsonl" >/dev/null; then
		test-ok "$host rejects invalid settings at the authored span before provisioning"
	else
		test-fail "$host invalid service settings: status=$status $(cat "$work/$host.invalid.err")"
	fi
done

for host in native wasm; do
	jq -s -e '
	  all(.[]; .schema == 1)
	  and (map(select(.type == "service-state")) as $events
	    | ($events | length) >= 6
	    and all($events[]; .target == "daemon" and (.generation | type) == "number" and (.attempt | type) == "number")
	    and all(["provisioning", "starting", "checking-ready", "ready", "checking-health", "restarting", "stopping", "terminal"][];
	      . as $expected | any($events[]; .state == $expected)))
	' "$work/$host.jsonl" >/dev/null || test-fail "$host JSON lifecycle states or correlation fields are incomplete"
done
jq -cs '[.[] | select(.type == "service-state") | [.target, .generation, .attempt, .state]] | reduce .[] as $e ([]; if any(.[]; .[3] == $e[3]) then . else . + [$e] end)' "$work/native.jsonl" >"$work/native.states"
jq -cs '[.[] | select(.type == "service-state") | [.target, .generation, .attempt, .state]] | reduce .[] as $e ([]; if any(.[]; .[3] == $e[3]) then . else . + [$e] end)' "$work/wasm.jsonl" >"$work/wasm.states"
if cmp -s "$work/native.states" "$work/wasm.states"; then
	test-ok "native and WASM JSON expose the same correlated lifecycle transitions"
else
	test-fail "native and WASM lifecycle JSON differ: native=$(cat "$work/native.states") wasm=$(cat "$work/wasm.states")"
fi

sed -n '/service /p' "$work/native.human.err" | sed -E 's/.*service ([^ ]+).*/\1/' | awk '!seen[$0]++' >"$work/native.human-states"
sed -n '/service /p' "$work/wasm.human.err" | sed -E 's/.*service ([^ ]+).*/\1/' | awk '!seen[$0]++' >"$work/wasm.human-states"
if cmp -s "$work/native.human-states" "$work/wasm.human-states"; then
	test-ok "native and WASM human output render matching lifecycle transitions"
else
	test-fail "native and WASM human lifecycle output differs"
fi

if jq -s -e 'all(.[]; (has("environment") or has("stdout") or has("stderr") or has("output")) | not)' "$work/native.jsonl" >/dev/null \
	&& jq -s -e 'all(.[]; (has("environment") or has("stdout") or has("stderr") or has("output")) | not)' "$work/wasm.jsonl" >/dev/null; then
	test-ok "service events omit process environment and output data"
else
	test-fail "service lifecycle events leaked process data"
fi

test-end
