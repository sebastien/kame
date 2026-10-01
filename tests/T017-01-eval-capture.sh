#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — Embedded commands, arguments, capture, execution context
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T017-01 embedded Kash command capture"
test-step "build native binary"
cli_require_tools
cli_build

evaluates() {
	cli_run -- do expr --allow-run -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

test-step "direct argv, quoting, typed expansions and nested parser boundaries"
evaluates '$(printf "%s" "John Smith")' '"John Smith"'
evaluates '$(printf "[%s]" "" "*.go" "a; echo injected")' '"[][*.go][a; echo injected]"'
evaluates '(let [files ["a b" "c"]] $(printf "[%s]" $files))' '"[a b][c]"'
evaluates '(let [name "unit"] $(printf "%s" ${name}.c))' '"unit.c"'
evaluates '$(printf "%s" @(cat "a" "b"))' '"ab"'
evaluates '$(printf "[%s]" @(["a b" "c"]))' '"[a b][c]"'
evaluates '$(printf "%s" @("a" | uppercase))' '"A"'
evaluates '$(printf "%s" $(printf nested))' '"nested"'
evaluates '(cat $(printf a) $(printf b))' '"ab"'
evaluates '(cat (eval "$(printf a)") (eval "$(printf b)"))' '"ab"'
evaluates '(let [f ([x] $(printf "%s" $x))] (cat (f "a") (f "b")))' '"ab"'
evaluates '(map ["a" "b"] ([x] $(printf "%s" $x)))' '["a" "b"]'
evaluates '[name: $(printf record)]' '[name: "record"]'
evaluates '"$(printf literal)"' '"$(printf literal)"'
evaluates '(if :false $(no-such-executable-kash) "selected")' '"selected"'
evaluates '$(printf "one\ntwo\n")' '"one\ntwo\n"' 
evaluates '$(printf "")' '""'
evaluates $'$(printf "[%s]" a \\\n b)' '"[a][b]"'

test-step "missing capabilities and failing commands remain failures"
cli_run -- do expr -c '$(printf denied)'
cli_expect_status 1
cli_expect_stderr_contains CAP_DENIED
cli_run -- do expr --allow-run -c '$(sh -c "printf diagnostic >&2; exit 7")'
cli_expect_status 1
cli_expect_stdout ''
cli_expect_stderr_contains diagnostic
cli_expect_stderr_contains RECIPE_FAIL
cli_run -- do expr --allow-run -c '$(no-such-executable-kash)'
cli_expect_status 1
cli_expect_stderr_contains HOST_FAIL
cli_run -- do expr --allow-run -c '(let [files ["a"]] $(printf "%s" "$files"))'
cli_expect_status 1
cli_expect_stderr_contains EXPR_INVALID
cli_run -- do expr --allow-run -c '$(printf "\\377")'
cli_expect_status 1
cli_expect_stderr_contains CAPTURE_ENCODING
cli_run -- do expr --allow-run -c '$(head -c 1048577 /dev/zero)'
cli_expect_status 1
cli_expect_stderr_contains CAPTURE_LIMIT

test-step "per-substitution capture limits and option validation"
cli_run -- do expr --allow-run --capture-limit 3 -c '$(printf abc)'
cli_expect_status 0
cli_expect_stdout '"abc"'
cli_run -- do expr --allow-run --capture-limit=3 -c '$(printf abcd)'
cli_expect_status 1
cli_expect_stdout ''
cli_expect_stderr_contains CAPTURE_LIMIT
cli_run -- do expr --allow-run --capture-limit=3 -c '(cat $(printf abc) $(printf def))'
cli_expect_status 0
cli_expect_stdout '"abcdef"'
cli_run -- do expr --allow-run --capture-limit=3 -c '$(printf "%s" $(printf abcd))'
cli_expect_status 1
cli_expect_stderr_contains CAPTURE_LIMIT
cli_run -- do expr --allow-run --capture-limit=3 -c '$(sh -c "printf abcdef >&2; printf abc")'
cli_expect_status 0
cli_expect_stdout '"abc"'
cli_expect_stderr_contains abcdef
cli_run -- do expr --allow-run --capture-limit=2 -c '$(printf "é")'
cli_expect_status 0
cli_expect_stdout '"é"'
cli_run -- do expr --allow-run --capture-limit=1 -c '$(printf "é")'
cli_expect_status 1
cli_expect_stderr_contains CAPTURE_LIMIT
for invalid in 0 -1 nope 999999999999999999999999 ''; do
	cli_run -- do expr "--capture-limit=$invalid" -c '$(printf unused)'
	cli_expect_status 2
	cli_expect_stderr_contains OPT_VALUE_INVALID
done
cli_run -- do expr --capture-limit
cli_expect_status 2
cli_expect_stderr_contains OPT_NO_VALUE

test-step "multiple substitutions are executed exactly once"
marker="$TMPDIR/capture-marker"
expression="(cat \$(sh -c \"printf x >> '$marker'; printf a\") \$(printf b))"
evaluates "$expression" '"ab"'
if [ "$(cat "$marker")" = x ]; then test-ok "completed substitution was not replayed"; else test-fail "substitution was replayed"; fi
expression="(map [1 1] ([x] \$(sh -c \"printf x >> '$marker'; printf a\")))"
evaluates "$expression" '["a" "a"]'
if [ "$(cat "$marker")" = xxx ]; then test-ok "identical callback arguments have distinct executions"; else test-fail "callback execution identity was lost"; fi

test-step "leading substitution RHS is a lazy Kame definition"
source_file="$TMPDIR/capture.kmk"
printf '%s\n' 'revision = $(printf revision)' 'unused = $(no-such-executable-kash)' >"$source_file"
cli_run -- -f "$source_file" revision
cli_expect_status 0
cli_expect_stdout '"revision"'
cli_expect_stderr_empty
cli_run -- --capture-limit=3 -f "$source_file" revision
cli_expect_status 1
cli_expect_stdout ''
cli_expect_stderr_contains CAPTURE_LIMIT

test-end
