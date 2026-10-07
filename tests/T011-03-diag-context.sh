#!/usr/bin/env bash
# Spec: docs/spec/011-diagnostics.md — precise operands and propagation context
# Spec: docs/spec/009-cli.md — target-scoped tools check
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T011-03 actionable diagnostic context"
test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "operation contracts identify operands instead of generic arguments"
cli_run -- do run --lang expr -c '(first 1)'
cli_expect_status 1
cli_expect_stderr_contains '<command:1>:1:8: error EXPR_INVALID: `first` argument 1 expects list; got int' 'while operation first'
if grep -q 'result = (nop' "$CLI_ERR"; then
	test-fail "expression diagnostic exposes the driver's synthetic wrapper"
else
	test-ok "expression diagnostic uses only authored input"
fi
cli_run -- do run --lang expr -c '(nth [1] :false)'
cli_expect_status 1
cli_expect_stderr_contains '`nth` argument 2 expects int; got bool'
cli_run -- do run --lang expr -c '(first)'
cli_expect_status 1
cli_expect_stderr_contains '`first` expects 1 argument; got 0'

test-step "recipe failures retain the operand span and full dependency path"
build=$'root : leaf\nleaf :\n\t@(first 1)\n'
cli_run -- --json -l kmk -c "$build" root
cli_expect_status 1
diagnostic="$(jq -c 'select(.type == "diagnostic") | .diagnostic' "$CLI_OUT")"
if jq -e '.code == "EXPR_INVALID" and .message == "`first` argument 1 expects list; got int" and .source == "<command:1>" and .target == "leaf" and .targetStack == ["root", "leaf"] and .frames[0].label == "first" and .frames[1].label == "leaf"' <<<"$diagnostic" >/dev/null; then
	test-ok "JSON retains precise failure and context"
else
	test-fail "unexpected diagnostic: $diagnostic"
fi
start="$(jq -r '.span.start' <<<"$diagnostic")"
end="$(jq -r '.span.end' <<<"$diagnostic")"
if [ "${build:start:end-start}" = 1 ]; then
	test-ok "primary span underlines only the rejected operand"
else
	test-fail "primary span is not the operand"
fi

test-step "unused tools do not block planning or execution"
tools=$'good :\n\ttrue\nbad :\n\t@(x/kame-definitely-missing-tool)\nroot : bad\n'
cli_run -- do plan -c "$tools" good
cli_expect_status 0
cli_run -- -l kmk -c "$tools" good
cli_expect_status 0
cli_run -- do tools check -c "$tools" good
cli_expect_status 0
cli_expect_stdout ''

test-step "explicit checks report the tool reference without executing recipes"
cli_run -- do tools check --json -c "$tools" root
cli_expect_status 1
cli_expect_json_query "$CLI_OUT" 'select(.type == "diagnostic") | .diagnostic.code' 'TOOL_MISSING'
cli_expect_json_query "$CLI_OUT" 'select(.type == "diagnostic") | .diagnostic.target' 'bad'
if jq -e -s 'map(select(.type == "diagnostic"))[0] | .diagnostic.targetStack == ["root", "bad"] and .diagnostic.source == "<command>" and (.diagnostic.tips | length) == 1' "$CLI_OUT" >/dev/null; then
	test-ok "tool check retains the dependency path and recovery action"
else
	test-fail "tool check omitted context"
fi
cli_run -- -l kmk -c "$tools" bad
cli_expect_status 1
cli_expect_stderr_contains 'TOOL_MISSING' '@(x/kame-definitely-missing-tool)' 'while rule bad'
cli_run -- do tools check -c "$tools"
cli_expect_status 2
cli_expect_stderr_contains 'tools check requires at least one target'

test-step "checks follow computed dependencies and reject cycles"
cli_run -- do tools check -c $'root : @("bad")\nbad :\n\t@(x/kame-definitely-missing-tool)\n' root
cli_expect_status 1
cli_expect_stderr_contains 'TOOL_MISSING' 'required by root -> bad'
cli_run -- do tools check -c $'a : b\nb : a\n' a
cli_expect_status 1
cli_expect_stderr_contains 'DEP_CYCLE' 'required by a -> b -> a'

test-step "read-only input expansion does not execute a recipe"
mkdir -p tool-project
cat >tool-project/Makefile.kmk <<'EOF'
root : @((wildcard ./*.txt))
	touch should-not-run
./source.txt :
	@(x/kame-definitely-missing-tool)
EOF
touch tool-project/source.txt
cli_run -- do tools check -C tool-project root
cli_expect_status 1
cli_expect_stderr_contains 'TOOL_MISSING'
if [ ! -e tool-project/should-not-run ]; then
	test-ok "tools check never executes recipes"
else
	test-fail "tools check executed a recipe"
fi

test-step "reached tools resolve relative startup PATH entries beneath -C"
mkdir -p tool-project/bin
cat >tool-project/bin/kame-test-tool <<'EOF'
#!/bin/sh
printf tool-ok
EOF
chmod +x tool-project/bin/kame-test-tool
PATH="bin:$PATH" cli_run -- do tools check -C tool-project -c $'using :\n\t@(x/kame-test-tool)\n' using
cli_expect_status 0
PATH="bin:$PATH" cli_run -- -C tool-project --env PATH=/does-not-exist -l kmk -c $'using :\n\t@(x/kame-test-tool)\n' using
cli_expect_status 0
cli_expect_stdout 'tool-ok'

test-step "included recipes and post-include rules retain authored locations"
mkdir -p include-project
cat >include-project/Makefile.kmk <<'EOF'
include ./rules.kmk
root : leaf
local :
	@(first 1)
EOF
cat >include-project/rules.kmk <<'EOF'
leaf :
	@(first 1)
EOF
cli_run -- -C include-project root
cli_expect_status 1
cli_expect_stderr_contains 'rules.kmk:2:17: error EXPR_INVALID:' 'required by root -> leaf'
cli_run -- -C include-project local
cli_expect_status 1
cli_expect_stderr_contains 'Makefile.kmk:4:17: error EXPR_INVALID:' '@(first 1)'

test-step "diagnostic causes omit captured output but retain outcome and capture metadata"
cli_run -- --json --log-limit 4 -l kmk -c $'fails :\n\tprintf diagnostic-secret-output\n\tprintf diagnostic-secret-error >&2\n\texit 7\n' fails
cli_expect_status 1
if jq -e -s 'map(select(.diagnostic.cause != null)) | length > 0 and all(.[]; .diagnostic.cause | .status == 7 and .stdoutLimit == 4 and .stderrLimit == 4 and .stdoutTruncated == true and .stderrTruncated == true and (has("stdout") | not) and (has("stderr") | not))' "$CLI_OUT" >/dev/null; then
	test-ok "causes contain metadata only"
else
	test-fail "cause retained captured output or lost metadata"
fi
if jq -e -s 'map(select(.type == "stdout" or .type == "stderr") | .data) | join("") | contains("diagnostic-secret-output") and contains("diagnostic-secret-error")' "$CLI_OUT" >/dev/null; then
	test-ok "explicit live streams remain unchanged"
else
	test-fail "diagnostic omission changed the live streams"
fi

test-end
