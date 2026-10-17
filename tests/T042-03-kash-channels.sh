#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — Slice C named channels
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-03 Kash named channel parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/channels"
mkdir -p "$work"

run_case() {
	local status=0
	timeout 20 "${command[@]}" 'do' run --lang kash --allow-run --allow-read --allow-write -c "$1" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = "$2" ]; then test-ok "$backend status"; else test-fail "$backend expected $2 got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "${3:-}" ]; then
		if grep -q "$3" "$work/$backend.err"; then test-ok "$backend: $3"; else test-fail "$backend missing $3: $(cat "$work/$backend.err")"; fi
	fi
}
expect() {
	local message="$1"
	shift
	if "$@"; then test-ok "$backend: $message"; else test-fail "$backend: $message"; fi
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	left="$work/$backend-left"
	right="$work/$backend-right"
	marker="$work/$backend-marker"
	test-step "$backend: named pipes, merges, and explicit broadcast"
	run_case "stream reports
(printf abc >&reports, &reports | cat > $left)" 0
	expect "named channel is a byte pipe" test "$(cat "$left")" = abc
	run_case "stream reports
((printf a, printf b) |(merge) >&reports, &reports | wc -c > $left)" 0
	expect "explicit merge has one named writer" test "$(tr -d '[:space:]' <"$left")" = 2
	run_case "stream left
stream right
(head -c 4194304 /dev/zero |(broadcast) (>&left, >&right), &left | cat > $left, &right | sh -c \"sleep 0.1; cat\" > $right)" 0
	expect "bounded named fan-out is lossless" cmp -s "$left" "$right"
	expect "named pipes stream beyond capture bounds" test "$(wc -c <"$left")" -eq 4194304

	test-step "$backend: filters, framed channels, and async ownership"
	run_case "stream selected
(printf \"a\nb\n\" |(frame :lines) |(filter (eq item \"a\") | cat >&selected) | cat > $right, &selected | cat > $left)" 0
	expect "selected branch owns its channel writer" test "$(cat "$left")" = a
	expect "named side output never rejoins implicitly" test "$(cat "$right")" = b
	run_case "stream records
(printf \"a\nb\n\" |(frame :lines) >&records, &records |(filter :true | cat > $left) | cat > $right) &" 0
	expect "framing crosses named aliases without encoding" test "$(cat "$left")" = $'a\nb'
	expect "async owner joins reader and selected branch" test ! -s "$right"
	run_case 'stream terminal
(printf abc >&terminal, &terminal)' 0
	expect "terminal channel reader presents bytes" grep -qx abc "$work/$backend.out"
	run_case "stream reports
if (eq 1 1)
    (printf inherited >&reports, &reports | cat > $left)" 0
	expect "control bodies inherit lexical declarations" test "$(cat "$left")" = inherited
	run_case "if (eq 1 1)
    stream local
    (printf local >&local, &local | cat > $left)
(printf escaped >&local, &local | cat > $right)" 1 KASH_CHANNEL_UNBOUND
	expect "body-local declaration executed its own graph" test "$(cat "$left")" = local

	test-step "$backend: validation precedes launch and truncation"
	printf preserve >"$left"
	run_case "stream reports
(sh -c \"touch $marker\" >&reports, printf b >&reports, &reports | cat > $left)" 1 KASH_CHANNEL_DUP_WRITER
	expect "duplicate writers launch no source" test ! -e "$marker"
	expect "duplicate writers preserve outputs" test "$(cat "$left")" = preserve
	run_case 'stream reports
(printf a >&reports, &reports | cat, &reports | cat)' 1 KASH_CHANNEL_DUP_READER
	run_case 'stream reports
printf a >&reports
&reports | cat' 1 KASH_CHANNEL_UNBOUND
	run_case '(printf a >&missing, &missing | cat)' 1 KASH_CHANNEL_UNBOUND
	run_case 'stream reports
&reports | cat' 1 KASH_CHANNEL_UNBOUND
	run_case 'stream first
stream second
(&first | cat >&second, &second | cat >&first)' 1 KASH_GRAPH_CYCLE
	run_case 'stream reports
(printf a >&reports, &reports |(filter :true | cat) | cat)' 1 KASH_FRAME_REQUIRED
	run_case 'stream reports
(:cwd . &reports | cat, printf a >&reports)' 1 PARSE_ERR
done
test-end
