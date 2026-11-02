#!/usr/bin/env bash
set -euo pipefail

# Bash reads scripts incrementally. Freeze this run before an editor can change
# file offsets underneath a long-running loop. Keep $0 for repository discovery.
if [ -z "${BASH_EXECUTION_STRING:-}" ]; then
	exec bash -c "$(<"$0")" "$0" "$@"
fi

BASE="$(cd "$(dirname "$0")" &>/dev/null && pwd)"
# Tests invoke make as independent clients, not recursive make recipes.
unset MAKEFLAGS MFLAGS MAKELEVEL
# shellcheck disable=SC1091
source "$BASE/lib-testing.sh"

# Give each worker its own process group, including its subprocesses.
set -m
declare -A running=()
progress_enabled=0
progress_owner=$BASHPID
harness_progress_emit() {
	[ "$progress_enabled" = 1 ] && [ "$BASHPID" = "$progress_owner" ] || return 0
	# OSC 9;4: normal (1), error (2), or remove (0). Never write into logs.
	printf '\033]9;4;%d;%d\033\134' "$1" "$2" >&2 || true
}
harness_progress_clear() {
	harness_progress_emit 0 0
}
trap 'harness_progress_clear; test_signal_exit' EXIT
cancel_tests() {
	local status="$1" pid
	local -a descendants=()
	trap '' INT TERM
	trap - EXIT ERR
	harness_progress_clear
	# Snapshot ancestry before killing parents: Python watch helpers use setsid,
	# so their children are not necessarily in a worker's process group.
	if [ "${#running[@]}" -gt 0 ]; then
		mapfile -t descendants < <(ps -eo pid=,ppid= | awk -v roots="${!running[*]}" '
			{ children[$2] = children[$2] " " $1 }
			function descend(pid, count, ids, i) {
				print pid
				count = split(children[pid], ids, " ")
				for (i = 1; i <= count; i++) if (ids[i] != "") descend(ids[i])
			}
			END { count = split(roots, ids, " "); for (i = 1; i <= count; i++) descend(ids[i]) }
		')
	fi
	for pid in "${!running[@]}"; do kill -TERM -- "-$pid" 2>/dev/null || true; done
	for pid in "${descendants[@]}"; do kill -TERM -- "$pid" 2>/dev/null || true; done
	# Bound shutdown even when a test or one of its subprocesses ignores TERM.
	if [ "${#running[@]}" -gt 0 ]; then
		sleep 1
		for pid in "${!running[@]}"; do kill -KILL -- "-$pid" 2>/dev/null || true; done
		for pid in "${descendants[@]}"; do
			kill -KILL -- "-$pid" "$pid" 2>/dev/null || true
		done
		wait 2>/dev/null || true
	fi
	test_log "Tests cancelled"
	test_cleanup
	exit "$status"
}
trap 'cancel_tests 130' INT
trap 'cancel_tests 143' TERM

run_setup() {
	# Use the same cancellable ownership for lock waits and artifact prebuilds.
	(set +m; "$@") &
	local pid=$! status=0
	running[$pid]=setup
	wait "$pid" || status=$?
	unset 'running[$pid]'
	return "$status"
}

usage() {
	echo "usage: harness.sh [-q|-v] [-j JOBS] [FILE...]" >&2
	echo "  -q  quiet: one summary line per test, failures dumped from the log" >&2
	echo "  -v  verbose: stream every assertion (historical behavior)" >&2
	echo "  -j  parallel-safe workers, then exclusive tests (default: KAME_TEST_JOBS or 4)" >&2
	echo "      verbose always uses 1 worker" >&2
}

# Older Bash can still run the complete suite sequentially.
JOBS=1
if help wait | grep -q -- '-p var'; then JOBS=4; fi
JOBS="${KAME_TEST_JOBS:-$JOBS}"

while [ $# -gt 0 ]; do
	case "$1" in
	-q | --quiet)
		test_set_verbosity quiet
		shift
		;;
	-v | --verbose)
		test_set_verbosity verbose
		shift
		;;
	-j | --jobs)
		[ $# -ge 2 ] || {
			usage
			exit 2
		}
		JOBS="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		break
		;;
	-*)
		usage
		exit 2
		;;
	*) break ;;
	esac
done

[[ "$JOBS" =~ ^[1-9][0-9]*$ ]] || {
	usage
	exit 2
}
[ "$TEST_VERBOSE" = 1 ] && JOBS=1
if [ "$JOBS" -gt 1 ] && ! help wait | grep -q -- '-p var'; then
	echo "parallel tests require Bash 5.1 or newer; use -j 1" >&2
	exit 2
fi

# Concurrent suite invocations share build artifacts and the retained log directory.
# Keep the suite lock in the parent; individual test scripts must not retain it.
mkdir -p "$BASE/../build/tests"
exec 8>"$BASE/../build/tests/.harness.lock"
run_setup flock -x 8

if [ $# == 0 ]; then
	mapfile -t FILES < <(find "$BASE" -maxdepth 1 -name "*.sh")
else
	FILES=("$@")
fi

TESTS=()
for TEST in "${FILES[@]}"; do
	case "$TEST" in
	*/lib-*.sh | */harness.sh | */data/*) ;;
	*/*.sh) TESTS+=("$TEST") ;;
	esac
done

parallel_count=0
if [ "$JOBS" -gt 1 ] && [ "${#TESTS[@]}" -gt 0 ]; then
	# Only explicitly reviewed scripts may share workers. Missing metadata is exclusive.
	declare -A parallel_safe=()
	if [ -f "$BASE/PARALLEL.tsv" ]; then
		while IFS=$'\t' read -r file reason; do
			case "$file" in '' | \#*) continue ;; esac
			if [[ "$file" != tests/*.sh ]] || [ ! -f "$BASE/../$file" ] || [ -z "$reason" ]; then
				echo "invalid parallel-safe test entry: $file (expected existing test and reason)" >&2
				exit 2
			fi
			parallel_safe["$(realpath "$BASE/../$file")"]=1
		done <"$BASE/PARALLEL.tsv"
	fi
	parallel=() exclusive=()
	for TEST in "${TESTS[@]}"; do
		if [ -n "${parallel_safe[$(realpath -m "$TEST")]:-}" ]; then
			parallel+=("$TEST")
		else
			exclusive+=("$TEST")
		fi
	done
	parallel_count=${#parallel[@]}
	# Longest-first applies only to the pool; exclusive tests keep their input order.
	mapfile -t TESTS < <(
		for TEST in "${parallel[@]}"; do
			duration=0
			cache="$TEST_RUNTIME_CACHE_DIR/$(basename "$TEST").seconds"
			if [ -r "$cache" ]; then IFS= read -r duration <"$cache" || true; fi
			[[ "$duration" =~ ^[0-9]+$ ]] || duration=0
			printf '%s\t%s\n' "$duration" "$TEST"
		done | sort -t $'\t' -k1,1nr -k2,2 | cut -f2-
	)
	TESTS+=("${exclusive[@]}")
	# Prebuild shared artifacts before concurrent standalone tests can race to build them.
	if [ "$parallel_count" -gt 0 ] && [ -z "${KAME_TEST_PREBUILT:-}" ]; then
		binary="$(realpath -m --relative-to="$BASE/.." "${CLI_BIN:-$BASE/../build/kame.debug}")"
		run_setup make -C "$BASE/.." "$binary" dist-wasm 8>&-
	fi
fi

# Per-test output is captured here and shown only for failures (or streamed
# live in verbose mode).
LOG_DIR="$BASE/../build/tests/logs"
mkdir -p "$LOG_DIR"
find "$LOG_DIR" -mindepth 1 -delete 2>/dev/null || true

test-start
eta_label=ETA
[ "$JOBS" -eq 1 ] || eta_label="Work remaining"
test_log "${DIM}$eta_label: $(test_runtime_eta "${TESTS[@]}")${RESET}"
if [ "$JOBS" -gt 1 ]; then
	test_log "${DIM}$parallel_count parallel-safe · $((${#TESTS[@]} - parallel_count)) exclusive${RESET}"
fi

pending=("${TESTS[@]}")
completed=0
total=${#TESTS[@]}
progress_weights=()
progress_total=0
progress_completed=0
progress_state=1
if [ -t 2 ] && [ "${TERM:-dumb}" != dumb ] && [ "$total" -gt 0 ]; then
	# Freeze ETA weights before workers update the runtime cache. No history
	# means equal weights; uncached tests otherwise use the same mean as ETA.
	average=$(test_runtime_average)
	for file in "${TESTS[@]}"; do
		weight=$average
		cache="$TEST_RUNTIME_CACHE_DIR/${file##*/}.seconds"
		if [ -r "$cache" ] && IFS= read -r cached <"$cache" && [[ "$cached" =~ ^[0-9]+$ ]]; then
			weight=$cached
		fi
		[ "$weight" -gt 0 ] || weight=1
		progress_weights+=("$weight")
		progress_total=$((progress_total + weight))
	done
	progress_enabled=1
	harness_progress_emit 1 0
fi
report_test() {
	local index="$1" status="$2" elapsed="$3" rel log progress eta
	rel="$(test-repo-path "${TESTS[$index]}")"
	log="$LOG_DIR/$rel.log"
	completed=$((completed + 1))
	progress=$(printf '%*d/%d' "${#total}" "$completed" "$total")
	test_runtime_cache_record "$rel" "$elapsed" || true
	unset 'pending[index]'
	eta="$(test_runtime_eta "${pending[@]}")"
	if [ "$status" -eq 0 ]; then
		[ "$TEST_VERBOSE" = 1 ] || test_log "${DIM}[$progress]${RESET} ${GREEN}EOK  ${RESET}${GREEN}${elapsed}s${RESET}  ${YELLOW}${rel}  ${DIM}$eta_label $eta${RESET}"
		test-ok
	else
		test_log "${DIM}[$progress]${RESET} ${RED}FAIL ${RESET}${RED}${elapsed}s${RESET}  ${YELLOW}${rel}  ${DIM}$eta_label $eta${RESET}"
		if [ "$TEST_VERBOSE" != 1 ] && [ -s "$log" ]; then
			test_log "${DIM}----- $rel -----${RESET}"
			while IFS= read -r line; do
				test_log "$line"
			done <"$log"
			test_log "${DIM}----- end $rel -----${RESET}"
		fi
		test-fail "Unit test failed: $rel"
	fi
	if [ "$TEST_VERBOSE" = 1 ]; then
		test_log "${DIM}ETA after $rel: $eta${RESET}"
	fi
	if [ "$progress_enabled" = 1 ]; then
		progress_completed=$((progress_completed + progress_weights[index]))
		[ "$status" -eq 0 ] || progress_state=2
		harness_progress_emit "$progress_state" "$((100 * progress_completed / progress_total))"
	fi
}

run_test() {
	# Descendants belong to this worker's group, not separate job-control groups.
	set +m
	local index="$1" started=$SECONDS status=0 rel log
	rel="$(test-repo-path "${TESTS[$index]}")"
	log="$LOG_DIR/$rel.log"
	mkdir -p "$(dirname "$log")"
	export TEST_COUNT
	if [ "$TEST_VERBOSE" = 1 ]; then
		[ "$TEST_COUNT" -le 1 ] || test_log_separator
		test-run "${DIM}»${PURPLE}" "${TESTS[$index]}" 8>&- || status=$?
	else
		env -C "$ORIGINAL_PATH" bash "${TESTS[$index]}" 8>&- >"$log" 2>&1 || status=$?
	fi
	printf '%s %s\n' "$status" "$((SECONDS - started))" >"$log.result"
}

next=0
if [ "$JOBS" -gt 1 ]; then
	while [ "$next" -lt "$parallel_count" ] || [ "${#running[@]}" -gt 0 ]; do
		while [ "$next" -lt "$parallel_count" ] && [ "${#running[@]}" -lt "$JOBS" ]; do
			run_test "$next" &
			running[$!]=$next
			next=$((next + 1))
		done
		finished=""
		wait -n -p finished "${!running[@]}" || true
		index=${running[$finished]}
		unset 'running[$finished]'
		status=1 elapsed=0
		read -r status elapsed <"$LOG_DIR/$(test-repo-path "${TESTS[$index]}").log.result" || true
		report_test "$index" "$status" "$elapsed"
	done
fi
# No worker is left running when an exclusive (or unclassified) test starts.
for ((index = next; index < ${#TESTS[@]}; index++)); do
	run_test "$index" &
	pid=$!
	running[$pid]=$index
	wait "$pid" || true
	unset 'running[$pid]'
	read -r status elapsed <"$LOG_DIR/$(test-repo-path "${TESTS[$index]}").log.result"
	report_test "$index" "$status" "$elapsed"
done

[ "$TEST_VERBOSE" = 1 ] || test_log_separator
if test-end; then
	echo "${GREEN}EOK${RESET}"
else
	echo "${RED}EFAIL${RESET}"
	test_cleanup
	exit 1
fi
# EOF
