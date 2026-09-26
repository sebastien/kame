#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Graph Inspection
# Spec: docs/spec/013-tests.md — T009-07-cli-graph
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-07 CLI graph inspection"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy project graph

test-step "inputs and outputs return stable JSON arrays of direct edges"
(
	cd graph
	cli_run -- do inputs ./build/app
	cli_expect_status 0
	cli_expect_stdout '["./build/main.o","./build/util.o"]
'
	cli_expect_stderr_empty

	cli_run -- do outputs ./build/app
	cli_expect_status 0
	cli_expect_stdout '["./build/app"]
'
)

test-step "depth controls edge traversal"
(
	cd graph
	cli_run -- do inputs --depth 0 ./build/app
	cli_expect_status 0
	cli_expect_stdout '[]
'

	cli_run -- do inputs --depth 1 ./build/app
	cli_expect_status 0
	cli_expect_stdout '["./build/main.o","./build/util.o"]
'

	cli_run -- do inputs --depth -1 ./build/app
	cli_expect_status 0
	cli_expect_stdout '["./build/main.o","./build/util.o","./src/main.c","./src/util.c"]
'

	cli_run -- do outputs --depth 2 ./build/app
	cli_expect_status 0
	cli_expect_stdout '["./build/app","./build/main.o","./build/util.o"]
'
)

test-step "span separates static and dynamic resources"
(
	cd graph
	cli_run -- do span ./build/app
	cli_expect_status 0
	cli_expect_jsonl "$CLI_OUT"
	cli_expect_json_query "$CLI_OUT" '.schema' '1'
	cli_expect_json_query "$CLI_OUT" '.type' 'span'
	cli_expect_json_query "$CLI_OUT" '.static.inputs | join(",")' './build/main.o,./build/util.o'
	cli_expect_json_query "$CLI_OUT" '.static.outputs | join(",")' './build/app'
	cli_expect_json_query "$CLI_OUT" '.dynamic | length' '0'
	cli_expect_json_query "$CLI_OUT" '.expanded' 'false'
	cli_expect_stderr_empty

	cli_run -- do span --expand ./build/app
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.expanded' 'true'

	cli_run -- do span --expand -c $'SOURCE = ./src/main.c\ntask inspect : @(SOURCE)\n\ttrue\n' inspect
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.static.inputs | length' '0'
	cli_expect_json_query "$CLI_OUT" '.dynamic | join(",")' './src/main.c'

	cli_run -- do span --expand --depth 2 -c $'SOURCE = ./src/main.c\ntask child : @(SOURCE)\n\ttrue\ntask inspect : child\n\ttrue\n' inspect
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.static.inputs | join(",")' 'child'
	cli_expect_json_query "$CLI_OUT" '.dynamic | join(",")' './src/main.c'

	mkdir -p expanded/src
	touch expanded/src/one.c expanded/src/two.c
	(
		cd expanded
		cli_run -- do span --expand -c $'task inspect : @((wildcard ./src/*.c))\n\tfalse\n' inspect
		cli_expect_status 0
		cli_expect_json_query "$CLI_OUT" '.dynamic | join(",")' './src/one.c,./src/two.c'
	)

	cli_run -- do span --depth 0 ./build/app
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.static.inputs | length' '0'
	cli_expect_json_query "$CLI_OUT" '.static.outputs | length' '0'

	cli_run -- do span --depth -1 ./build/app
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.static.inputs | index("./src/main.c") != null' 'true'
	cli_expect_json_query "$CLI_OUT" '.static.outputs | index("./build/main.o") != null' 'true'
)

test-step "graph commands validate depth and arity"
(
	cd graph
	cli_run -- do inputs --depth nope ./build/app
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"

	cli_run -- do inputs --depth -2 ./build/app
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"

	cli_run -- do inputs --expand ./build/app
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_UNKNOWN"

	cli_run -- do inputs
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"

	cli_run -- do outputs ./build/app ./build/main.o
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"
)

test-step "graph commands report unknown targets"
(
	cd graph
	cli_run -- do inputs ./missing.txt
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE"

	cli_run -- do span ./missing.txt
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE"
	cli_expect_printable "$CLI_ERR"
)

test-end
