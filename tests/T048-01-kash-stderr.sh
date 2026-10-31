#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md
# shellcheck disable=SC1091
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-01 Kash stderr routing"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	check() {
		local expected_out="$1" expected_err="$2" source="$3" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected_out" ] && [ "$(cat "$work/err")" = "$expected_err" ]; then test-ok "$backend $expected_out"; else
			test-fail "$backend output (status=$status, stdout=$(printf '%q' "$(cat "$work/out")") stderr=$(printf '%q' "$(cat "$work/err")"))"
		fi
	}
	reject() {
		local code="$1" source="$2" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend rejects $code"; else
			test-fail "$backend expected $code ($status)"
			cat "$work/err"
		fi
	}
	test-step "$backend stderr files and alias order"
	check '' '' 'sh -c "printf out; printf err >&2" > ./merged 2>&1'
	if [ "$(cat "$work/merged")" = outerr ]; then test-ok "$backend merged file"; else test-fail "$backend merged file $(printf '%q' "$(cat "$work/merged")")"; fi
	check err '' 'sh -c "printf out; printf err >&2" 2>&1 > ./stdout-only'
	if [ "$(cat "$work/stdout-only")" = out ]; then test-ok "$backend alias keeps previous stdout"; else test-fail "$backend alias order $(printf '%q' "$(cat "$work/stdout-only")")"; fi
	check '' '' 'sh -c "printf out; printf err >&2" > ./kept 2> ./errs'
	if [ "$(cat "$work/kept")" = out ] && [ "$(cat "$work/errs")" = err ]; then test-ok "$backend separate stderr file"; else test-fail "$backend separate stderr file"; fi
	printf old >"$work/appended"
	check '' '' 'sh -c "printf more >&2" 2>> ./appended'
	if [ "$(cat "$work/appended")" = oldmore ]; then test-ok "$backend stderr append"; else test-fail "$backend stderr append"; fi
	check outerr '' 'sh -c "printf out; printf err >&2" 2>&1 | cat'
	check err '' 'value = $(sh -c "printf err >&2" 2>&1)
printf "%s" $value'
	check '<>' '' 'value = $(sh -c "printf out; printf err >&2" > ./captured 2>&1)
printf "<%s>" $value'
	if [ "$(cat "$work/captured")" = outerr ]; then test-ok "$backend file alias is not captured"; else test-fail "$backend file alias capture"; fi
	test-step "$backend alias follows the reserved channels"
	check a '' 'printf a >&out 2>&1'
	check '' a 'printf a >&err 2>&1'
	check oe '' 'sh -c "printf o; printf e >&2" >&out 2>&1'
	check '' oe 'sh -c "printf o; printf e >&2" >&err 2>&1'
	test-step "$backend capture and ambiguity"
	check ab '' 'value = $(printf a 2>&1 && printf b 2>&1)
printf "%s" $value'
	reject KASH_AMBIGUOUS_CAPTURE 'value = $((printf a 2>&1, printf b 2>&1))
printf "%s" $value'
	reject PARSE_ERR 'printf a > ./conflict | cat'
	check piped '' 'sh -c "printf piped; printf err >&2" 2> ./side | cat'
	if [ "$(cat "$work/side")" = err ]; then test-ok "$backend stderr file does not steal the pipe"; else test-fail "$backend pipe and stderr file"; cat "$work/out" "$work/side"; fi
	reject PARSE_ERR 'printf a 3> ./nope'
	reject PARSE_ERR 'printf a 1>&2'
	reject PARSE_ERR 'printf a 2>&-'
	check '' '' 'printf "%s" 2 > ./argument'
	if [ "$(cat "$work/argument")" = 2 ]; then test-ok "$backend spaced 2 is an argument"; else test-fail "$backend spaced descriptor"; fi
	printf preserve >"$work/preserve"
	status=0
	"${runner[@]}" do run -C "$work" --allow-run -l kash -c 'printf x 2> ./ungranted' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -e "$work/ungranted" ]; then test-ok "$backend denied stderr write opens nothing"; else test-fail "$backend denied stderr ($status)"; cat "$work/err"; fi
	if [ "$(cat "$work/preserve")" = preserve ]; then test-ok "$backend preflight left an existing file"; else test-fail "$backend truncated before rejection"; fi
	test-step "$backend tee copies without hiding status"
	status=0
	invoke -l kash -c 'sh -c "printf out; printf err >&2; exit 7" 2>&1 |(tee ./tee-log)' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 7 ] && [ "$(cat "$work/out")" = outerr ] && [ "$(cat "$work/tee-log")" = outerr ]; then test-ok "$backend tee keeps command status"; else
		test-fail "$backend tee status=$status stdout=$(printf '%q' "$(cat "$work/out")") file=$(printf '%q' "$(cat "$work/tee-log" 2>/dev/null)")"
		cat "$work/err"
	fi
	printf old >"$work/tee-append"
	check more '' 'printf more |(tee :append ./tee-append)'
	if [ "$(cat "$work/tee-append")" = oldmore ]; then test-ok "$backend tee append"; else test-fail "$backend tee append $(printf '%q' "$(cat "$work/tee-append")")"; fi
done
test-end
