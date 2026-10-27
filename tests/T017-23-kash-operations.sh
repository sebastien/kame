#!/usr/bin/env bash
# Specs: docs/spec/017-kash.md and
#        docs/spec/047-kash-output-ports-and-command-lists.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-23 operational Kash contracts"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	test-step "$backend environments and captures preserve exact bytes"
	payload=$'  synthetic-credential-sentinel\t"\x27$; | &\r\nline two\n\n'
	printf '%s' "$payload" >"$work/payload"
	invoke -l kash -c 'exports = [TOKEN: (text (read ./payload))]; :env $exports sh -c "printf %s \"\$TOKEN\""' >"$work/received" 2>"$work/err"
	if cmp -s "$work/payload" "$work/received"; then test-ok "$backend exact environment bytes"; else test-fail "$backend environment altered data"; fi
	invoke -l kash -c '(write! ./captured $(cat < ./payload))' >"$work/out" 2>"$work/err"
	if cmp -s "$work/payload" "$work/captured"; then test-ok "$backend exact capture bytes"; else test-fail "$backend capture altered data"; fi
	for value in '' $'\n\n' $'\r\n' $' \t'; do
		printf '%s' "$value" >"$work/value"
		invoke -l kash -c '(write! ./captured $(cat < ./value))' >"$work/out" 2>"$work/err"
		if cmp -s "$work/value" "$work/captured"; then test-ok "$backend empty/newline/whitespace capture"; else test-fail "$backend capture framing"; fi
	done
	test-step "$backend computed environments stay out of JSON traces and failures"
	invoke --json -l kash -c 'exports = [TOKEN: (text (read ./payload))]; :env $exports sh -c "printf %s \"\$TOKEN\" > private-received"' >"$work/trace" 2>"$work/err"
	if cmp -s "$work/payload" "$work/private-received" && ! grep -q synthetic-credential-sentinel "$work/trace" "$work/err"; then test-ok "$backend trace excludes computed environment"; else test-fail "$backend environment disclosure"; fi
	for record in '[TOKEN: [(text (read ./payload))]]' '(parse-json "{\"bad=name\":\"synthetic-credential-sentinel\"}")' '[TOKEN: (text (read ./nul))]'; do
		printf '\0' >"$work/nul"
		status=0
		invoke --json -l kash -c "sh -c \"touch forbidden\" | :env (nop $record) true" >"$work/trace" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/trace" && ! grep -q synthetic-credential-sentinel "$work/trace" "$work/err" && [ ! -e "$work/forbidden" ]; then test-ok "$backend validates entire graph without disclosure"; else test-fail "$backend invalid env launched or leaked"; cat "$work/trace" "$work/err"; fi
	done
	test-step "$backend capture errors never truncate successful values or embed captured data"
	for scenario in limit encoding; do
		if [ "$scenario" = limit ]; then input=payload; code=CAPTURE_LIMIT; options=(--capture-limit 1); else printf '\377' >"$work/invalid"; input=invalid; code=CAPTURE_ENCODING; options=(); fi
		printf previous >"$work/protected"
		status=0
		invoke "${options[@]}" -l kash -c "(write! ./protected \$(cat < ./$input))" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ "$(cat "$work/protected")" = previous ] && ! grep -q synthetic-credential-sentinel "$work/err"; then test-ok "$backend $scenario fails without publication/disclosure"; else test-fail "$backend unsafe capture failure"; fi
	done
	test-step "$backend accepts status 1 only after explicit ? and rejects fatal/signal/missing-tool cases"
	for exit_code in 1 2; do
		status=0
		invoke -l kash -c "attempt = (kash \"\"\"sh -c \"exit $exit_code\" ?\"\"\"); result = (attempt); (if (and (= result.signal 0) (has? result.status [0 1])) :nil (fail! \"fatal status\"))" >"$work/out" 2>"$work/err" || status=$?
		if [ "$exit_code:$status" = 1:0 ] || { [ "$exit_code:$status" = 2:1 ] && grep -q USER_ERROR "$work/err"; }; then test-ok "$backend selected status $exit_code"; else test-fail "$backend status policy"; cat "$work/err"; fi
	done
	status=0
	invoke -l kash -c 'attempt = (kash """kame-deliberately-missing-executable ?"""); (attempt)' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q TOOL_MISSING "$work/err"; then test-ok "$backend ? does not accept missing tools"; else test-fail "$backend missing tool policy"; fi
	status=0
	invoke -l kash -c 'attempt = (kash """sh -c "kill -TERM \$\$" ?"""); result = (attempt); (if (and (= result.signal 0) (has? result.status [0 1])) :nil (fail! "signal rejected"))' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'signal rejected' "$work/err"; then test-ok "$backend selected-status policy rejects signals"; else test-fail "$backend signal status policy"; fi
	test-step "$backend ordered finite work, failure collection and fresh bounded retry"
	cat >"$work/probe" <<'SH'
#!/bin/sh
printf '%s' "$1" >> attempts
case "$1" in 1|2) exit 75;; *) exit 0;; esac
SH
	chmod +x "$work/probe"
	invoke -l kash -c 'probe = (kash """./probe $args ?"""); result = (reduce ([previous number] (if (= previous.status 0) previous (if (and (= previous.signal 0) (= previous.status 75)) (probe number) (fail! "fatal")))) [status: 75 signal: 0] [1 2 3 4]); (if (= result.status 0) :nil (fail! "exhausted"))' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/attempts")" = 123 ]; then test-ok "$backend fresh attempts stop after success"; else test-fail "$backend retry replay/memoization"; fi
	rm "$work/attempts"
	invoke -l kash -c 'probe = (kash """./probe $args ?"""); results = (map probe [1 2 3]); printf "%s" (json (map ([result] result.status) results))' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/attempts")" = 123 ] && [ "$(cat "$work/out")" = '[75,75,0]' ]; then test-ok "$backend explicit failure collection"; else test-fail "$backend result collection"; fi
	rm "$work/attempts"
	status=0
	invoke -l kash -c '(map ([number] (run! "./probe" number)) [1 2 3])' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 75 ] && [ "$(cat "$work/attempts")" = 1 ]; then
		test-ok "$backend map remains fail-fast and preserves exit status"
	else
		test-fail "$backend map ignored failure or changed exit status"
	fi
	test-step "$backend cookbook lists and output ports run and clean up"
	invoke -l kash "$CLI_ROOT/examples/kash/04-operations.kash" >"$work/out" 2>"$work/err"
	printf 'typed argv\nfirst\nsecond\nrecovered\nfallback\ncaptured=payload\n' >"$work/expected"
	printf 'progress\n' >"$work/expected-err"
	if cmp -s "$work/out" "$work/expected" \
		&& cmp -s "$work/err" "$work/expected-err" \
		&& ! compgen -G "$work/kash-example.*" >/dev/null; then
		test-ok "$backend operational example"
	else
		test-fail "$backend cookbook example"
		cat "$work/out" "$work/err"
	fi
done
test-end
