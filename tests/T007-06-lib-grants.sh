#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Shell Operations, Environment
# Spec: docs/spec/009-cli.md — Expression
# Spec: docs/spec/013-tests.md — T007-06-lib-grants
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T007-06 library capabilities and shell"

test-step "toolchain and binary"
cli_require_tools
cli_build

operation_fixture_copy lib-grants lib-grants

test-step "capabilities are denied by default"
for expr in '(read "a.txt")' '(write "x.txt" "x")' '(env "HOME")' '(shell "true")' '(sh "true")' '(shellrun "true")' '(shell-template ["true " ""] ["x"])'; do
	cli_run --dir lib-grants -- do run --lang expr -c "$expr"
	cli_expect_status 1 "$expr"
	cli_expect_stderr_contains 'CAP_DENIED'
done

test-step "unrestricted grants permit operations"
cli_run --dir lib-grants -- do run --lang expr --allow-read -c '(read "a.txt")'
cli_expect_status 0
cli_expect_stdout "alpha
"

cli_run --dir lib-grants -- do run --lang expr --allow-write -c '(write "written.txt" "data")'
cli_expect_status 0
cli_expect_file lib-grants/written.txt 'data'

test-step "rooted grants limit filesystem access"
cli_run --dir lib-grants -- do run --lang expr "--allow-read=$PWD/lib-grants" -c '(read "a.txt")'
cli_expect_status 0
cli_expect_stdout "alpha
"

cli_run --dir lib-grants -- do run --lang expr "--allow-read=$PWD/lib-grants" -c '(read "../secret.txt")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

cli_run --dir lib-grants -- do run --lang expr "--allow-write=$PWD/lib-grants" -c '(write "ok.txt" "ok")'
cli_expect_status 0

cli_run --dir lib-grants -- do run --lang expr "--allow-write=$PWD/lib-grants" -c '(write "../outside.txt" "no")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-step "wildcard cannot escape the granted root"
cli_run --dir lib-grants -- do run --lang expr "--allow-read=$PWD/lib-grants" -c '(wildcard "../*.txt")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'
cli_expect_stderr_contains '--allow-read=ROOT'

test-step "wildcard denial names the required grant"
cli_run --dir lib-grants -- do run --lang expr -c '(wildcard "*.txt")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED' '--allow-read=ROOT'
cli_run --dir lib-grants -- do run --lang expr --allow-read=. -c '(wildcard "*.txt")'
cli_expect_status 0
cli_expect_stdout_contains 'a.txt'

test-step "environment grants scope variable names"
cli_run --dir lib-grants -- do run --lang expr --allow-env -c '(env "HOME")'
cli_expect_status 0
cli_expect_stdout "\"$TEST_PATH\""

cli_run --dir lib-grants -- do run --lang expr --allow-env=HOME -c '(env "HOME")'
cli_expect_status 0
cli_expect_stdout "\"$TEST_PATH\""

cli_run --dir lib-grants -- do run --lang expr --allow-env=OTHER_NAME -c '(env "HOME")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-step "shell captures status and separate streams"
cli_run --dir lib-grants -- do run --lang expr --allow-run -c '(shell "echo out; echo err >&2; exit 3")'
cli_expect_status 0
cli_expect_stdout_contains 'status: 3' 'stdout: out' 'stderr: err'

cli_run --dir lib-grants -- do run --lang expr --allow-run -c '(shell "echo ok")'
cli_expect_status 0
cli_expect_stdout_contains 'status: 0' 'stdout: ok'

cli_run --dir lib-grants -- do run --lang expr --allow-run -c '(sh "echo ok")'
cli_expect_status 0
cli_expect_stdout_contains 'status: 0' 'stdout: ok'

cli_run --dir lib-grants -- do run --lang expr --allow-run -c '(shellrun "echo ok")'
cli_expect_status 0
cli_expect_stdout_contains 'status: 0' 'stdout: ok'

test-step "shell-template quotes interpolated values as one shell argument"
template_expr='(shell-template ["printf %s " ""] ["a'"'"'b; touch injected; #"])'
cli_run --dir lib-grants -- do run --lang expr --allow-run -c "$template_expr"
cli_expect_status 0
cli_expect_stdout_contains "status: 0" "stdout: a'b; touch injected; #"
cli_expect_no_file lib-grants/injected

make -C "$CLI_ROOT" dist-wasm >/dev/null
wasm_out="$TEST_PATH/shell-template-wasm.out"
wasm_err="$TEST_PATH/shell-template-wasm.err"
if (cd lib-grants && node "$CLI_ROOT/dist/kame.js" do run --lang expr --allow-run -c "$template_expr") >"$wasm_out" 2>"$wasm_err"; then
	if grep -Fq "stdout: a'b; touch injected; #" "$wasm_out" && [ ! -e lib-grants/injected ] && [ ! -s "$wasm_err" ]; then
		test-ok "WASM quotes dynamic values without executing shell syntax"
	else
		test-fail "WASM shell-template output or injection guard failed: $(cat "$wasm_out" "$wasm_err")"
	fi
else
	test-fail "WASM shell-template failed: $(cat "$wasm_out" "$wasm_err")"
fi

cli_run --dir lib-grants -- do run --lang expr --allow-run -c '(shell-template ["echo " ""] ["x" "y"])'
cli_expect_status 1
cli_expect_stderr_contains 'EXPR_INVALID'

test-step "shell is denied without the run capability"
cli_run --dir lib-grants -- do run --lang expr -c '(shell "echo hi")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-end
