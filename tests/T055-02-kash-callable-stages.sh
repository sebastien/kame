#!/usr/bin/env bash
# Spec: docs/spec/055-unified-kame-kash-language.md — functions as process stages
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T055-02 Kash callable stages"
cd "$CLI_ROOT"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi

for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	check() {
		local expected="$1" error="$2" status=0
		timeout -k 2 20 "${runner[@]}" do run -C "$work" --lang kash "$work/stage.kash" <"${3:-/dev/null}" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && cmp -s <(printf '%s' "$expected") "$work/out" &&
			cmp -s <(printf '%s' "$error") "$work/err"; then
			test-ok "$backend stdout and stderr match"
		else
			test-fail "$backend unexpected stage output ($status)"
			cat "$work/out" "$work/err"
		fi
	}

	test-step "$backend calls typed functions and keeps output channels separate"
	cat >"$work/stage.kash" <<'KASH'
def describe value
	err! "notice\n"
	out! (fmt value) "\n"
	return "ignored"
describe ([1, true]) | cat
describe ({label: "direct"}) > ./redirected 2> ./notice
cat ./redirected ./notice
KASH
	check $'[1, true]\n{label: "direct"}\nnotice\n' $'notice\n'

	test-step "$backend reads stage stdin through record loops"
	cat >"$work/stage.kash" <<'KASH'
def decorate prefix
	for line from :stdin :lines
		out! $prefix "[" $line "]"
printf "a\r\n\nlast" | decorate x | cat
KASH
	check 'x[a]x[]x[last]' ''

	test-step "$backend connects subprocesses inside a callable to its ports"
	cat >"$work/stage.kash" <<'KASH'
def transform ()
	out! "["
	tr a-z A-Z | tr 0-9 X
	let local = $(printf "private")
	out! $local "]"
	sh -c 'printf "notice" >&2'
printf "abc1" | transform > ./transformed 2> ./transform-error
cat ./transformed ./transform-error
KASH
	check '[ABCXprivate]notice' ''

	test-step "$backend can read records after a subprocess uses callable stdin"
	cat >"$work/stage.kash" <<'KASH'
def transform ()
	head -c 1
	for line from :stdin :lines
		out! "[" $line "]"
printf "ab\nlast" | transform | cat
KASH
	check 'a[b][last]' ''

	test-step "$backend hands off stdin after a record loop without dropping the tail"
	cat >"$work/stage.kash" <<'KASH'
def transform ()
	for line from :stdin :nul
		out! "[" $line "]"
		break
	cat
printf 'first\000tail' | transform | cat
KASH
	check '[first]tail' ''

	test-step "$backend lends the callable input to a nested callable graph and takes it back"
	cat >"$work/stage.kash" <<'KASH'
def first ()
	for line from :stdin :lines
		out! "[" $line "]"
		break
def transform ()
	first | cat
	cat
transform < ./items | cat
printf "three\nfour\n" | transform | cat
KASH
	printf 'one\ntwo\n' >"$work/items"
	check $'[one]two\n[three]four\n' ''

	test-step "$backend hands off the tail after a record spans multiple exchanges"
	cat >"$work/stage.kash" <<'KASH'
def transform ()
	for line from :stdin :lines
		out! (fmt (count line))
		break
	cat
transform < ./large-record | cat
KASH
	{ head -c 65535 /dev/zero | tr '\0' x; printf '\ntail'; } >"$work/large-record"
	check '65535tail' ''

	if [ "$(uname -s)" = Linux ]; then
		test-step "$backend cancels a record loop waiting after a subprocess uses stdin"
		cat >"$work/stage.kash" <<'KASH'
def transform ()
	defer (write! ./exact-retired "retired")
	head -c 1
	for line from :stdin :lines
		out! $line
let result = $?(transform < ./exact-pipe | sh -c 'head -c 1 >/dev/null; sleep 0.1; exit 7')
out! (fmt result.status)
KASH
		mkfifo "$work/exact-pipe"
		exec {stdin_fd}<>"$work/exact-pipe"
		printf p >&"$stdin_fd"
		check '7' ''
		exec {stdin_fd}>&-
		cli_expect_file "$work/exact-retired" 'retired'
	fi

	test-step "$backend keeps subprocess output and errors in their enclosing capture"
	cat >"$work/stage.kash" <<'KASH'
def transform ()
	printf abc | tr a-z A-Z
	sh -c 'printf "notice" >&2'
let result = $?(transform | cat)
out! $result.value
KASH
	check 'ABC' 'notice'

	test-step "$backend streams nested subprocess output larger than a pipe buffer"
	cat >"$work/stage.kash" <<'KASH'
def transfer ()
	cat
head -c 262144 /dev/zero | transfer | wc -c
KASH
	check $'262144\n' ''

	test-step "$backend cancels a nested subprocess when a sibling fails"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./child-retired "retired")
	sh -c 'printf "ready\n"; sleep 30'
let result = $?(produce | sh -c 'IFS= read -r line; exit 7')
out! (fmt result.status)
KASH
	check '7' ''
	cli_expect_file "$work/child-retired" 'retired'

	test-step "$backend cancels a callable subprocess producer when downstream stops"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./output-retired "retired")
	yes x
produce | head -c 1
KASH
	check 'x' ''
	cli_expect_file "$work/output-retired" 'retired'

	test-step "$backend gives a callable borrowed stdin and leaves unread bytes for the caller"
	cat >"$work/stage.kash" <<'KASH'
def first ()
	for line from :stdin :lines
		out! "[" $line "]"
		break
first | cat
first < ./redirect | cat
cat
KASH
	printf 'one\ntwo\n' >"$work/input"
	printf 'file\n' >"$work/redirect"
	check $'[one][file]two\n' '' "$work/input"

	test-step "$backend yields generator bytes without converting values and stops on broken output"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (err! "cleaned\n")
	while true
		out! "abcdefghij\n"
		yield 42
produce | head -c 1
out! " after"
KASH
	check 'a after' $'cleaned\n'

	test-step "$backend stops an active generator when a sibling fails"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./retired "retired")
	out! "ready\n"
	while true
		yield 42
let result = $?(produce | sh -c 'IFS= read -r line; exit 7')
out! (fmt result.status)
KASH
	check '7' ''
	cli_expect_file "$work/retired" 'retired'

	test-step "$backend stops a silent generator when downstream exits successfully"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./silent-retired "retired")
	out! "ready\n"
	while true
		yield 42
let result = $?(produce | sh -c 'IFS= read -r line')
let producer = (first result.stages)
out! (fmt result.status) ":" (fmt producer.outcome) ":" (fmt producer.state) ":" (fmt producer.value)
KASH
	check '0:2::cancelled:nil' ''
	cli_expect_file "$work/silent-retired" 'retired'

	test-step "$backend stops a generator blocked in a subprocess when the consumer exits"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./blocked-child-retired "retired")
	out! "ready\n"
	yield 1
	sh -c 'touch ./blocked-child-started; sleep 30; touch ./blocked-child-continued'
let result = $?(produce | sh -c 'IFS= read -r line; while [ ! -f ./blocked-child-started ]; do sleep 0.01; done')
let producer = (first result.stages)
out! (fmt result.status) ":" (fmt producer.state)
KASH
	check '0::cancelled' ''
	cli_expect_file "$work/blocked-child-retired" 'retired'
	if [ -e "$work/blocked-child-continued" ]; then
		test-fail "$backend allowed the cancelled subprocess to continue"
	fi

	test-step "$backend detects closed stdin before the consuming process exits"
	cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./closed-live-retired "retired")
	out! "ready\n"
	yield 1
	sh -c 'touch ./closed-live-started; sleep 30'
let result = $?(produce | sh -c 'IFS= read -r line; while [ ! -f ./closed-live-started ]; do sleep 0.01; done; exec 0<&-; while [ ! -f ./closed-live-retired ]; do sleep 0.01; done; printf consumer-finished')
let producer = (first result.stages)
out! $result.value ":" (fmt result.status) ":" (fmt producer.state)
KASH
	mkdir "$work/watch-tmp"
	TMPDIR="$work/watch-tmp" check 'consumer-finished:0::cancelled' ''
	cli_expect_file "$work/closed-live-retired" 'retired'
	if compgen -G "$work/watch-tmp/kame-pipes-*" >/dev/null; then
		test-fail "$backend left pipeline watch paths after joining the graph"
	fi

	if [ "$(uname -s)" = Linux ]; then
		test-step "$backend stops a generator blocked on a stage FIFO when the consumer exits"
		cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./blocked-fifo-retired "retired")
	out! "ready\n"
	yield 1
	for line from :stdin :lines
		yield line
let result = $?(produce < ./blocked-input | sh -c 'IFS= read -r line; sleep 0.1')
let producer = (first result.stages)
out! (fmt result.status) ":" (fmt producer.state)
KASH
		mkfifo "$work/blocked-input"
		exec {stdin_fd}<>"$work/blocked-input"
		check '0::cancelled' ''
		exec {stdin_fd}>&-
		cli_expect_file "$work/blocked-fifo-retired" 'retired'

		test-step "$backend releases borrowed stdin when a successful consumer exits"
		cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./borrowed-fifo-retired "retired")
	out! "ready\n"
	yield 1
	for line from :stdin :lines
		yield line
produce | sh -c 'IFS= read -r line; sleep 0.1'
out! "complete"
KASH
		mkfifo "$work/borrowed-input"
		exec {stdin_fd}<>"$work/borrowed-input"
		check 'complete' '' "$work/borrowed-input"
		exec {stdin_fd}>&-
		cli_expect_file "$work/borrowed-fifo-retired" 'retired'

		test-step "$backend releases a callable waiting on borrowed stdin when a sibling fails"
		cat >"$work/stage.kash" <<'KASH'
def produce ()
	defer (write! ./reader-retired "retired")
	out! "ready\n"
	for line from :stdin :lines
		yield line
produce | sh -c 'IFS= read -r line; sleep 0.1; exit 7' ?
KASH
		mkfifo "$work/pipe"
		exec {stdin_fd}<>"$work/pipe"
		check '' '' "$work/pipe"
		exec {stdin_fd}>&-
		cli_expect_file "$work/reader-retired" 'retired'
	fi

	test-step "$backend captures callable stages, handles failures and awaits async graphs"
	cat >"$work/stage.kash" <<'KASH'
def say text
	out! $text
def fail-stage ()
	(fail! "stage failed")
let result = $?(say captured | cat)
out! $result.value
let failure = $?(fail-stage | cat)
let failedStage = (first failure.stages)
out! (fmt failure.status) (fmt failedStage.state) $failedStage.error.message ":" (fmt failedStage.value)
let job = $&(say background | cat)
let background = $?(wait $job)
out! $background.value
KASH
	check 'captured1:failedstage failed:nilbackground' ''

	test-step "$backend retains typed stage returns, including closures, through capture and async wait"
	cat >"$work/stage.kash" <<'KASH'
def make-return base
	out! "bytes"
	return {callback: (x => base + x), data: [base, :typed]}
def show-result result argument
	let stage = (first result.stages)
	let callback = stage.value.callback
	out! $result.value ":" (fmt stage.value.data) ":" (fmt (callback argument)) "\n"
(show-result $?(make-return (3) | cat) 2)
let job = $&(make-return (10) | cat)
(show-result $?(wait $job) 2)
(show-result $?(wait $job) 3)
(show-result $?(printf prefix && make-return (7) | cat) 2)
KASH
	check $'bytes:[3, :typed]:5\nbytes:[10, :typed]:12\nbytes:[10, :typed]:13\nprefixbytes:[7, :typed]:9\n' ''
done
test-end
