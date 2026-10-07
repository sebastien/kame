#!/usr/bin/env bash

# --
# CLI conformance helpers for the compiled kame binary.
#
# Sourced by tests/T*-*.sh after tests/lib-testing.sh. Never executed as a
# test: tests/harness.sh skips tests/lib-*.sh.
#
# Spec: docs/spec/013-tests.md

set -euo pipefail

# A caller CDPATH makes 'cd' echo the directory and pollutes captures.
unset CDPATH

# Resolve the repository root from this file, not from the caller.
CLI_ROOT="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." &>/dev/null && pwd)"

# Variable: CLI_BIN
# Debug build of the CLI under test (checks enabled, warnings reported).
CLI_BIN="${CLI_BIN:-$CLI_ROOT/build/kame.debug}"

# Variable: KAME
# Convenience alias used by tests.
KAME="$CLI_BIN"
export CLI_BIN KAME

# Variable: CLI_SOURCES
# Directories whose changes require a rebuild of CLI_BIN.
CLI_SOURCES=("$CLI_ROOT/src/go/kame/cli" "$CLI_ROOT/src/go/kame/cmd" "$CLI_ROOT/src/go/kame/core" "$CLI_ROOT/src/go/kame/diagnostic" "$CLI_ROOT/src/go/kame/host" "$CLI_ROOT/src/go/kame/lang" "$CLI_ROOT/src/go/kame/operations" "$CLI_ROOT/src/go/kame/program")

# Variable: CLI_OUT, CLI_ERR, CLI_STATUS
# Captures from the most recent cli_run invocation.
CLI_OUT=""
CLI_ERR=""
CLI_STATUS=""

# -----------------------------------------------------------------------------
#
# SETUP
#
# -----------------------------------------------------------------------------

# Function: cli_require_tools
# Fails the current test when a required host tool is missing.
function cli_require_tools {
	local missing=()
	local tool
	for tool in so jq timeout flock sha256sum ps touch mktemp diff realpath readlink; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			missing+=("$tool")
		fi
	done
	if [ "${#missing[@]}" != 0 ]; then
		test-fail "missing required tools: ${missing[*]}"
		exit 1
	fi
	test-ok "host tools available"
}

# Function: cli_sources_newer BINARY
# Reports success (0) when a production source is newer than BINARY. Unit
# packages and Go-only test files do not participate in the native executable.
function cli_sources_newer {
	local binary="$1"
	local dir
	for dir in "${CLI_SOURCES[@]}"; do
		if [ ! -d "$dir" ]; then
			continue
		fi
		if [ -n "$(find "$dir" -type f ! -path '*/test/*' ! -name '*_test.go' -newer "$binary" -print -quit 2>/dev/null)" ]; then
			return 0
		fi
	done
	local module
	for module in "$CLI_ROOT/src/go/kame/go.mod" "$CLI_ROOT/src/go/kame/go.sum"; do
		if [ -e "$module" ] && [ "$module" -nt "$binary" ]; then
			return 0
		fi
	done
	return 1
}

# Function: cli_build
# Builds the debug CLI once per suite; a no-op when the binary is current.
function cli_build {
	if ! command -v so >/dev/null 2>&1; then
		test-fail "the 'so' toolchain is not available"
		exit 1
	fi
	"$CLI_ROOT/tools/generate-version.sh"
	if [ -x "$CLI_BIN" ] && ! cli_sources_newer "$CLI_BIN"; then
		test_log_message "CLI binary is current: $(test-relpath "$CLI_BIN")"
		return 0
	fi
	local lock="$CLI_ROOT/build/.kame.debug.lock"
	mkdir -p "$CLI_ROOT/build"
	test_log_message "building CLI: $(test-relpath "$CLI_BIN")"
	# Serialize concurrent suite runs; the build takes ~25s.
	if ! (
		flock -x 9 || exit 1
		if [ -x "$CLI_BIN" ] && ! cli_sources_newer "$CLI_BIN"; then
			exit 0
		fi
		if [ "$CLI_BIN" = "$CLI_ROOT/build/kame.debug" ]; then
			make -C "$CLI_ROOT" build/kame.debug >&2 || exit 1
		else
			cd "$CLI_ROOT/src/go/kame" || exit 1
			if [[ "$CLI_BIN" == *.sanitize ]]; then
				CC=clang CFLAGS="${CFLAGS:--O2} -DKAME_BUILD_MODE_SANITIZE" so build -check=sanitize -panic=abort -o "$CLI_BIN" ./cmd/kame >&2 || exit 1
			else
				so build -check=warn -o "$CLI_BIN" ./cmd/kame >&2 || exit 1
			fi
		fi
	) 9>"$lock"; then
		test-fail "cannot build the CLI binary"
		exit 1
	fi
	if [ ! -x "$CLI_BIN" ]; then
		test-fail "CLI binary was not produced: $CLI_BIN"
		exit 1
	fi
	test-ok "CLI binary built: $(test-relpath "$CLI_BIN")"
}

# -----------------------------------------------------------------------------
#
# INVOCATION
#
# -----------------------------------------------------------------------------

# Function(internal): _cli_new_capture PREFIX
function _cli_new_capture {
	local prefix="$1"
	mktemp -p "$TEST_PATH" "$prefix.XXXXXX"
}

# Function: cli_run [OPTIONS] [--] ARG…
#
# Runs the CLI under test and captures status, stdout and stderr.
#
# Options:
#   --dir DIR      working directory (default: current)
#   --stdin FILE   standard input (default: /dev/null)
#   --timeout SECS safety timeout (default: CLI_TIMEOUT or 30)
#   --env K=V      extra environment entry (repeatable)
#   --             end of helper options; remaining values are CLI arguments
#
# Sets CLI_STATUS, CLI_OUT, CLI_ERR. Never aborts the test on nonzero status.
function cli_run {
	local dir="$PWD"
	local stdin_file="/dev/null"
	local timeout_s="${CLI_TIMEOUT:-30}"
	local envs=()
	while [ $# -gt 0 ]; do
		case "$1" in
		--dir)
			dir="$2"
			shift 2
			;;
		--stdin)
			stdin_file="$2"
			shift 2
			;;
		--timeout)
			timeout_s="$2"
			shift 2
			;;
		--env)
			envs+=("$2")
			shift 2
			;;
		--)
			shift
			break
			;;
		*)
			break
			;;
		esac
	done
	CLI_OUT="$(_cli_new_capture cli.out)"
	CLI_ERR="$(_cli_new_capture cli.err)"
	local had_errexit=false
	if [[ $- == *e* ]]; then
		had_errexit=true
		set +e
	fi
	# A clean environment keeps recipe behavior independent from the caller.
	(
		cd "$dir" && exec env -i \
			PATH="$PATH" \
			HOME="$TEST_PATH" \
			TMPDIR="$TEST_PATH" \
			LC_ALL=C \
			TZ=UTC \
			NO_COLOR=1 \
			ASAN_OPTIONS="${ASAN_OPTIONS:-}" \
			UBSAN_OPTIONS="${UBSAN_OPTIONS:-}" \
			${envs[@]+"${envs[@]}"} \
			timeout --signal=TERM "$timeout_s" "$CLI_BIN" "$@"
	) <"$stdin_file" >"$CLI_OUT" 2>"$CLI_ERR"
	CLI_STATUS=$?
	if [ "$had_errexit" = true ]; then
		set -e
	fi
	return 0
}

# Variable: CLI_SPAWN_PID
# Pid of the most recent cli_spawn background process.
CLI_SPAWN_PID=""

# Function: cli_spawn DIR ARG…
# Starts the CLI in the background and sets CLI_SPAWN_PID. Callers own the pid
# and should reap it or register cleanup; run without command substitution so
# the process stays a child of the caller's shell.
function cli_spawn {
	local dir="$1"
	shift
	(
		cd "$dir" && exec env -i \
			PATH="$PATH" \
			HOME="$TEST_PATH" \
			TMPDIR="$TEST_PATH" \
			LC_ALL=C \
			TZ=UTC \
			NO_COLOR=1 \
			ASAN_OPTIONS="${ASAN_OPTIONS:-}" \
			UBSAN_OPTIONS="${UBSAN_OPTIONS:-}" \
			"$CLI_BIN" "$@"
	) >"$TEST_PATH/spawn.out" 2>"$TEST_PATH/spawn.err" &
	CLI_SPAWN_PID=$!
}

# Function: cli_wait PID
# Waits for PID and returns its exit status; safe under errexit.
function cli_wait {
	local pid="$1"
	local had_errexit=false
	local status=0
	if [[ $- == *e* ]]; then
		had_errexit=true
		set +e
	fi
	wait "$pid"
	status=$?
	if [ "$had_errexit" = true ]; then
		set -e
	fi
	return "$status"
}

# -----------------------------------------------------------------------------
#
# STREAM AND STATUS ASSERTIONS
#
# -----------------------------------------------------------------------------

# Function(internal): _cli_diff EXPECTED_FILE ACTUAL_FILE MESSAGE
function _cli_diff {
	local expected="$1"
	local actual="$2"
	local message="$3"
	test_log "${ORANGE}--- expected${RESET}"
	test_log_run "${ORANGE}---" sed "s/^/    /" "$expected"
	test_log "${ORANGE}--- actual${RESET}"
	test_log_run "${ORANGE}---" sed "s/^/    /" "$actual"
	test-fail "$message"
}

function cli_expect_status { # WANT [MESSAGE]
	local want="$1"
	if [ "$CLI_STATUS" = "$want" ]; then
		test-ok "${2:-exit status $want}"
	else
		test-fail "exit status $CLI_STATUS, wanted $want${2:+ ($2)}"
	fi
}

function cli_expect_status_nonzero { # [MESSAGE]
	if [ "$CLI_STATUS" != 0 ]; then
		test-ok "${1:-exit status nonzero ($CLI_STATUS)}"
	else
		test-fail "exit status 0, wanted nonzero${1:+ ($1)}"
	fi
}

function cli_expect_stdout { # TEXT [MESSAGE]
	local want="$1"
	local want_file
	want_file="$(_cli_new_capture cli.want)"
	printf '%s' "$want" >"$want_file"
	if cmp -s "$want_file" "$CLI_OUT"; then
		test-ok "${2:-stdout matches}"
	else
		_cli_diff "$want_file" "$CLI_OUT" "${2:-stdout differs}"
	fi
}

function cli_expect_stdout_file { # FILE [MESSAGE]
	local want="$1"
	if cmp -s "$want" "$CLI_OUT"; then
		test-ok "${2:-stdout matches $(test-relpath "$want")}"
	else
		_cli_diff "$want" "$CLI_OUT" "${2:-stdout differs from $(test-relpath "$want")}"
	fi
}

function cli_expect_stdout_matches { # REGEX [MESSAGE]
	local pattern="$1"
	if grep -Eq "$pattern" "$CLI_OUT"; then
		test-ok "${2:-stdout matches $pattern}"
	else
		test-fail "${2:-stdout does not match $pattern}: $(test_fmt_line "$(cat "$CLI_OUT")")"
	fi
}

function cli_expect_stdout_empty { # [MESSAGE]
	if [ -s "$CLI_OUT" ]; then
		test-fail "${1:-stdout is not empty}: $(test_fmt_line "$(cat "$CLI_OUT")")"
	else
		test-ok "${1:-stdout empty}"
	fi
}

function cli_expect_stderr { # TEXT [MESSAGE]
	local want="$1"
	local want_file
	want_file="$(_cli_new_capture cli.want)"
	printf '%s' "$want" >"$want_file"
	if cmp -s "$want_file" "$CLI_ERR"; then
		test-ok "${2:-stderr matches}"
	else
		_cli_diff "$want_file" "$CLI_ERR" "${2:-stderr differs}"
	fi
}

function cli_expect_stderr_empty { # [MESSAGE]
	if [ -s "$CLI_ERR" ]; then
		test-fail "${1:-stderr is not empty}: $(test_fmt_line "$(cat "$CLI_ERR")")"
	else
		test-ok "${1:-stderr empty}"
	fi
}

function cli_expect_stderr_file { # FILE [MESSAGE]
	local want="$1"
	if cmp -s "$want" "$CLI_ERR"; then
		test-ok "${2:-stderr matches $(test-relpath "$want")}"
	else
		_cli_diff "$want" "$CLI_ERR" "${2:-stderr differs from $(test-relpath "$want")}"
	fi
}

function cli_expect_stderr_contains { # PATTERN… [MESSAGE]
	local patterns=("$@")
	local pattern
	local failed=0
	for pattern in "${patterns[@]}"; do
		if ! grep -Fq -- "$pattern" "$CLI_ERR"; then
			test-fail "stderr does not contain: $pattern"
			failed=1
		fi
	done
	if [ "$failed" = 0 ]; then
		test-ok "stderr contains ${#patterns[@]} pattern(s)"
	fi
}

function cli_expect_stdout_contains { # PATTERN… [MESSAGE]
	local patterns=("$@")
	local pattern
	local failed=0
	for pattern in "${patterns[@]}"; do
		if ! grep -Fq -- "$pattern" "$CLI_OUT"; then
			test-fail "stdout does not contain: $pattern"
			failed=1
		fi
	done
	if [ "$failed" = 0 ]; then
		test-ok "stdout contains ${#patterns[@]} pattern(s)"
	fi
}

# Function: cli_expect_printable FILE
# Reject invalid UTF-8 and non-printable controls; human presentation permits Unicode.
function cli_expect_printable {
	local file="$1"
	if ! python3 - "$file" <<'PYPRINT'
import pathlib, sys
try:
    text = pathlib.Path(sys.argv[1]).read_text(encoding='utf-8')
except UnicodeDecodeError:
    sys.exit(1)
sys.exit(0 if all(char.isprintable() or char in '\t\r\n' for char in text) else 1)
PYPRINT
	then
		test-fail "$(test-relpath "$file") contains non-printable bytes"
	else
		test-ok "$(test-relpath "$file") is printable"
	fi
}

# -----------------------------------------------------------------------------
#
# FILESYSTEM ASSERTIONS
#
# -----------------------------------------------------------------------------

function cli_expect_file { # PATH [TEXT]
	local path="$1"
	if [ ! -e "$path" ]; then
		test-fail "missing file: $(test-relpath "$path")"
		return 1
	fi
	if [ $# -ge 2 ]; then
		local want_file
		want_file="$(_cli_new_capture cli.want)"
		printf '%s' "$2" >"$want_file"
		if cmp -s "$want_file" "$path"; then
			test-ok "file content: $(test-relpath "$path")"
		else
			_cli_diff "$want_file" "$path" "file content differs: $(test-relpath "$path")"
		fi
		return 0
	fi
	test-ok "file exists: $(test-relpath "$path")"
}

function cli_expect_file_bytes { # PATH EXPECTED_FILE
	local path="$1"
	local want="$2"
	if [ ! -e "$path" ]; then
		test-fail "missing file: $(test-relpath "$path")"
		return 1
	fi
	if cmp -s "$want" "$path"; then
		test-ok "file bytes: $(test-relpath "$path")"
	else
		_cli_diff "$want" "$path" "file bytes differ: $(test-relpath "$path")"
	fi
}

function cli_expect_no_file { # PATH
	local path="$1"
	if [ -e "$path" ]; then
		test-fail "unexpected file: $(test-relpath "$path")"
	else
		test-ok "absent: $(test-relpath "$path")"
	fi
}

function cli_expect_dir { # PATH
	local path="$1"
	if [ -d "$path" ]; then
		test-ok "directory exists: $(test-relpath "$path")"
	else
		test-fail "missing directory: $(test-relpath "$path")"
	fi
}

# -----------------------------------------------------------------------------
#
# JSON ASSERTIONS
#
# -----------------------------------------------------------------------------

# Function: cli_expect_jsonl FILE
# Every non-empty line must be a standalone JSON value.
function cli_expect_jsonl {
	local file="$1"
	local line
	local number=0
	local failed=0
	while IFS= read -r line || [ -n "$line" ]; do
		number=$((number + 1))
		if [ -z "$line" ]; then
			continue
		fi
		if ! jq -e . >/dev/null 2>&1 <<<"$line"; then
			test-fail "line $number is not JSON: $(test_fmt_line "$line")"
			failed=1
			break
		fi
	done <"$file"
	if [ "$failed" = 0 ]; then
		test-ok "$(test-relpath "$file"): $number JSON line(s)"
	fi
}

# Function: cli_json_types FILE [FILTER]
# Prints one event type per line (FILTER defaults to .type).
function cli_json_types {
	local file="$1"
	local filter="${2:-.type}"
	jq -r "$filter" "$file"
}

# Function: cli_expect_json_query FILE FILTER EXPECTED [MESSAGE]
function cli_expect_json_query {
	local file="$1"
	local filter="$2"
	local want="$3"
	local actual
	actual="$(jq -r "$filter" "$file" 2>/dev/null)" || {
		test-fail "jq filter failed: $filter"
		return 1
	}
	if [ "$actual" = "$want" ]; then
		test-ok "${4:-$filter = $want}"
	else
		test-fail "$filter = $(test_fmt_line "$actual"), wanted $(test_fmt_line "$want")"
	fi
}

# Function: cli_expect_event FILE TYPE [JQ_PREDICATE]
# Requires at least one event of TYPE (and satisfying the predicate).
function cli_expect_event {
	local file="$1"
	local type="$2"
	local predicate="${3:-true}"
	local count
	count="$(jq -s "[.[] | select(.type == \"$type\" and ($predicate))] | length" "$file" 2>/dev/null)" || count=0
	if [ "${count:-0}" -gt 0 ]; then
		test-ok "event $type present"
	else
		test-fail "event $type absent (predicate: $predicate)"
	fi
}

# Function: cli_expect_diagnostic FILE CODE [SEVERITY]
# Checks code and severity presence, a non-empty message, and printable output.
function cli_expect_diagnostic {
	local file="$1"
	local code="$2"
	local severity="${3:-error}"
	if ! grep -Fq -- "$code" "$file"; then
		test-fail "diagnostic $code not found in $(test-relpath "$file")"
		return 1
	fi
	if ! grep -Eq -- "(^|[^A-Za-z_])${severity}([^A-Za-z_]|$)" "$file"; then
		test-fail "diagnostic $code without severity $severity"
		return 1
	fi
	cli_expect_printable "$file"
}

# Function: cli_normalize_json FILE
# Emits a stable projection of JSON Lines, dropping volatile identity fields.
function cli_normalize_json {
	local file="$1"
	jq -cS 'del(.node, .request, .generation, .attempt)' "$file"
}

# -----------------------------------------------------------------------------
#
# FIXTURES
#
# -----------------------------------------------------------------------------

function tests_data_path { # RELATIVE
	echo -n "$CLI_ROOT/tests/data/$1"
}

function lang_fixture { # RELATIVE (under tests/data/lang)
	tests_data_path "lang/$1"
}

function fixture_path { # NAME (under tests/data/cli)
	tests_data_path "cli/$1"
}

# Function: fixture_copy NAME [DEST]
# Copies fixture NAME into DEST (default: current directory).
function fixture_copy {
	local name="$1"
	local dest="${2:-.}"
	local src
	src="$(fixture_path "$name")"
	if [ ! -d "$src" ]; then
		test-fail "unknown fixture: $name"
		return 1
	fi
	mkdir -p "$dest"
	cp -a "$src/." "$dest/"
	test_log_message "fixture $name → $(test-relpath "$dest")"
}

# Function: operation_fixture_copy NAME [DEST]
# Copies a standard-library fixture NAME into DEST (default: current directory).
function operation_fixture_copy {
	local name="$1"
	local dest="${2:-.}"
	local src
	src="$(tests_data_path "operations/$name")"
	if [ ! -d "$src" ]; then
		test-fail "unknown operation fixture: $name"
		return 1
	fi
	mkdir -p "$dest"
	cp -a "$src/." "$dest/"
	test_log_message "operation fixture $name → $(test-relpath "$dest")"
}

# Function: case_path NAME
# Absolute path of one table-driven case directory.
function case_path {
	tests_data_path "cases/$1"
}

# -----------------------------------------------------------------------------
#
# PROCESS AND TIME HELPERS
#
# -----------------------------------------------------------------------------

# Function: set_mtime FILE EPOCH
function set_mtime {
	local file="$1"
	local epoch="$2"
	touch -d "@$epoch" "$file"
}

# Function: run_count FILE
function run_count {
	local file="$1"
	if [ ! -e "$file" ]; then
		echo -n "0"
	else
		wc -l <"$file" | tr -d ' '
	fi
}

# Function: wait_for_file FILE [SECONDS]
# Bounded poll for FILE to exist. Returns 0 on success.
function wait_for_file {
	local file="$1"
	local deadline="${2:-10}"
	local tenths=$((deadline * 10))
	while [ ! -e "$file" ]; do
		if [ "$tenths" -le 0 ]; then
			return 1
		fi
		sleep 0.1
		tenths=$((tenths - 1))
	done
	return 0
}

# Function: wait_for_pid_gone PID [SECONDS]
# Bounded poll until PID no longer accepts signals. Returns 0 when gone.
function wait_for_pid_gone {
	local pid="$1"
	local deadline="${2:-10}"
	local tenths=$((deadline * 10))
	while [ "$tenths" -gt 0 ]; do
		if ! kill -0 "$pid" 2>/dev/null; then
			return 0
		fi
		local state
		state="$(ps -o stat= -p "$pid" 2>/dev/null || true)"
		if [ -z "$state" ]; then
			return 0
		fi
		sleep 0.1
		tenths=$((tenths - 1))
	done
	return 1
}

# Variable: CLI_WAIT_STATUS
# Result of the most recent wait_for_status call.
CLI_WAIT_STATUS=""

# Function: wait_for_status PID [SECONDS]
# Polls a background process and sets CLI_WAIT_STATUS to "exited STATUS" or
# "running". Run without command substitution so wait can reap the child.
function wait_for_status {
	local pid="$1"
	local deadline="${2:-10}"
	local tenths=$((deadline * 10))
	while [ "$tenths" -gt 0 ]; do
		if ! kill -0 "$pid" 2>/dev/null; then
			local status=0
			wait "$pid" || status=$?
			CLI_WAIT_STATUS="exited $status"
			return 0
		fi
		sleep 0.1
		tenths=$((tenths - 1))
	done
	CLI_WAIT_STATUS="running"
	return 0
}

# Function: kill_tree PID
# Best-effort cleanup for a leaked process group.
function kill_tree {
	local pid="$1"
	if kill -0 "$pid" 2>/dev/null; then
		kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
	fi
}

# EOF
