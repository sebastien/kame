#!/usr/bin/env bash
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$(dirname "$(realpath "${BASH_SOURCE[0]}")")")"/tests/lib-testing.sh

usage() {
	echo "usage: harness.sh [-q|-v] [FILE...]" >&2
	echo "  -q  quiet: one summary line per test, failures dumped from the log" >&2
	echo "  -v  verbose: stream every assertion (historical behavior)" >&2
}

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

if [ $# == 0 ]; then
	FILES=$(find "$BASE" -name "*.*")
else
	FILES=$*
fi

# Per-test output is captured here and shown only for failures (or streamed
# live in verbose mode).
LOG_DIR="$BASE/../build/tests/logs"
mkdir -p "$LOG_DIR"
find "$LOG_DIR" -mindepth 1 -delete 2>/dev/null || true

test-start

for TEST in $FILES; do
	case "$TEST" in
	*/lib-*.sh) ;;
	*/harness.sh) ;;
	*/data/*) ;;
	*/*.sh)
		export TEST_COUNT
		rel="$(test-repo-path "$TEST")"
		log="$LOG_DIR/$rel.log"
		mkdir -p "$(dirname "$log")"
		started=$SECONDS
		status=0
		if [ "$TEST_VERBOSE" = 1 ]; then
			if [ "$TEST_COUNT" -gt 1 ]; then
				test_log_separator
			fi
			test-run "${DIM}»${PURPLE}" "$TEST" || status=$?
		else
			env -C "$ORIGINAL_PATH" "$SHELL" "$TEST" >"$log" 2>&1 || status=$?
		fi
		elapsed=$((SECONDS - started))
		if [ "$status" -eq 0 ]; then
			[ "$TEST_VERBOSE" = 1 ] || test_log "${GREEN}EOK  ${RESET}${GREEN}${elapsed}s${RESET}  ${YELLOW}${rel}"
			test-ok
		else
			test_log "${RED}EFAIL ${RESET}${RED}${elapsed}s${RESET}  ${YELLOW}${rel}"
			if [ "$TEST_VERBOSE" != 1 ] && [ -s "$log" ]; then
				test_log "${DIM}----- $rel -----${RESET}"
				while IFS= read -r line; do
					test_log "$line"
				done <"$log"
				test_log "${DIM}----- end $rel -----${RESET}"
			fi
			test-fail "Unit test failed: $rel"
		fi
		;;
	esac
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
