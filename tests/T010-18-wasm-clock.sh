#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — clock operations
# Spec: docs/spec/010-wasm.md — time host requests
# Spec: docs/spec/013-tests.md — T010-18-wasm-clock
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-18 wasm clock host requests"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-clock"
mkdir -p "$project"

wasm() {
	set +e
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" "$@") >"$project/out.txt" 2>"$project/err.txt"
	status=$?
	set -e
}

native() {
	set +e
	(cd "$project" && "$CLI_BIN" "$@") >"$project/nout.txt" 2>"$project/nerr.txt"
	nstatus=$?
	set -e
}

test-step "now returns epoch nanoseconds on both backends"
wasm do run --lang expr -c '(now)'
native do run --lang expr -c '(now)'
if [ "$status" = 0 ] && [ "$nstatus" = 0 ] && [[ "$(cat "$project/out.txt")" =~ ^1[0-9]{18}$ ]] && [[ "$(cat "$project/nout.txt")" =~ ^1[0-9]{18}$ ]]; then
	# Both readings are within a minute of each other.
	drift=$(( $(cat "$project/nout.txt") - $(cat "$project/out.txt") ))
	if [ "$drift" -lt 0 ]; then drift=$(( -drift )); fi
	if [ "$drift" -lt 60000000000 ]; then
		test-ok "now matches native within a minute"
	else
		test-fail "now drift: native=$(cat "$project/nout.txt") wasm=$(cat "$project/out.txt")"
	fi
else
	test-fail "now: status=$status native=$nstatus out=$(cat "$project/out.txt")"
fi

test-step "monotonic is a non-negative, non-decreasing nanosecond clock"
wasm do run --lang expr -c '(monotonic)'
first="$(cat "$project/out.txt")"
wasm do run --lang expr -c '(monotonic)'
second="$(cat "$project/out.txt")"
if [ "$status" = 0 ] && [ "$first" -ge 0 ] && [ "$second" -ge "$first" ]; then
	test-ok "monotonic readings are non-negative and non-decreasing"
else
	test-fail "monotonic: first=$first second=$second"
fi

test-step "clocks need no capability grant"
wasm do run --lang expr -c '(now)'
if [ "$status" = 0 ] && [ ! -s "$project/err.txt" ]; then
	test-ok "now runs under the default deny-all grants"
else
	test-fail "now grant: status=$status err=$(cat "$project/err.txt")"
fi

test-step "a clock definition materializes on both backends"
cat >"$project/Makefile.kmk" <<'EOF'
STAMP = (now)
EOF
wasm do cat STAMP
native do cat STAMP
if [ "$status" = 0 ] && [ "$nstatus" = 0 ] && [[ "$(cat "$project/out.txt")" =~ ^1[0-9]{18}$ ]] && [[ "$(cat "$project/nout.txt")" =~ ^1[0-9]{18}$ ]]; then
	test-ok "a (now) definition materializes to epoch nanoseconds"
else
	test-fail "clock definition: status=$status native=$nstatus out=$(cat "$project/out.txt")"
fi

test-end
