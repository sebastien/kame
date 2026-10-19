#!/usr/bin/env bash
# Spec: docs/spec/044-value-producing-rule-bodies.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T044-01 value-producing rule bodies"
cd "$CLI_ROOT"
cli_build
make dist-wasm >/dev/null

for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	printf 'Ada\n' >"$work/name"
	printf 'binary\000\377\n' >"$work/binary"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
MODE = kame
default : ./greeting ./bytes ./empty ./page
./greeting : ./name ; [shell: MODE]
	(str "Hello, "
	  (strip (text (read @<))) "!\n")
./bytes : ./binary ; [shell: kame]
	(read @<)
./empty : ./name ; [shell: kame]
	""
./page : ./greeting ; [shell: kame]
	(render [message: (strip (text (read @<)))] "plain" ./page.txt)
./shell : ./name ; [shell: /bin/sh]
	printf '%s' '@(strip (text (read @<)))' > @>
KMK
	printf 'Message: @{message}\n' >"$work/page.txt"
	test-step "$backend publishes exact bytes without processes"
	"${runner[@]}" --json -n -C "$work" >"$work/dry.json" 2>"$work/err"
	if [ ! -e "$work/greeting" ] && jq -e -s 'all(.type != "process-started")' "$work/dry.json" >/dev/null; then test-ok "$backend dry-run does not publish or execute"; else test-fail "$backend dry-run performed work"; fi
	"${runner[@]}" --json -C "$work" >"$work/events.json" 2>"$work/err"
	printf 'Hello, Ada!\n' >"$work/expected"
	if cmp -s "$work/greeting" "$work/expected" && cmp -s "$work/bytes" "$work/binary" && [ -f "$work/empty" ] && [ ! -s "$work/empty" ] && [ "$(cat "$work/page")" = 'Message: Hello, Ada!' ] && jq -e -s 'all(.type != "process-started")' "$work/events.json" >/dev/null; then test-ok "$backend value bodies publish text, binary, empty and rendered content"; else test-fail "$backend artifact bytes or interpreter selection"; cat "$work/err"; fi
	"${runner[@]}" --json -C "$work" ./greeting >"$work/reuse.json" 2>"$work/err"
	if jq -e -s 'any(.type == "target-completed") and all(.type != "effect" and .type != "process-started")' "$work/reuse.json" >/dev/null; then test-ok "$backend reuses unchanged content"; else test-fail "$backend failed to reuse content"; fi
	printf 'Lin\n' >"$work/name"
	"${runner[@]}" -C "$work" ./page ./shell >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/page")" = 'Message: Hello, Lin!' ] && [ "$(cat "$work/shell")" = Lin ]; then test-ok "$backend tracks reads and retains pure shell interpolation"; else test-fail "$backend dependency invalidation"; fi

	test-step "$backend rejects effects directly and through lazy definitions"
	for body in '(yield! "bad")' '(out! "bad")' '(err! "bad")' '(write! ./side "bad")' '$(touch side)' '(sh! "touch side")' '(now)' 'LAZY' '(helper)'; do
		for header in './greeting : ./name ; [shell: kame]' './greeting : ./name ; [shell: /bin/sh]'; do
			if [[ "$header" == *'shell: kame'* ]]; then expression="$body"; else expression="@${body}"; [ "$body" != LAZY ] || expression='@{LAZY}'; fi
			if [[ "$header" != *'shell: kame'* && "$body" == '$('* ]]; then expression="@(nop $body)"; fi
			printf 'LAZY = (sh! "touch side")\n(helper) = (out! "bad")\n%s\n\t%s\n' "$header" "$expression" >"$work/Makefile.kmk"
			status=0
			"${runner[@]}" -C "$work" ./greeting >"$work/out" 2>"$work/err" || status=$?
			if [ "$status" = 1 ] && grep -q PHASE_INVALID "$work/err" && [ ! -s "$work/out" ] && [ ! -e "$work/side" ] && [ "$(cat "$work/greeting")" = 'Hello, Lin!' ]; then test-ok "$backend rejects $body without replacing the artifact"; else test-fail "$backend accepted effect $body"; cat "$work/err"; fi
		done
	done
done
test-end
