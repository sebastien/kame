#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Invocation, Diagnostics
# Spec: docs/spec/013-tests.md — T009-12-cli-cases
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-12 table-driven CLI cases"

test-step "toolchain and binary"
cli_require_tools
cli_build

# Function: read_lines FILE
# Prints one line per entry; absent files print nothing.
read_lines() {
	local file="$1"
	if [ -f "$file" ]; then
		cat "$file"
	fi
}

for dir in "$(tests_data_path cases)"/*; do
	[ -d "$dir" ] || continue
	name="$(basename "$dir")"
	test-step "case $name"

	args=()
	while IFS= read -r line || [ -n "$line" ]; do
		args+=("$line")
	done <"$dir/args"

	stdin_file="/dev/null"
	if [ -f "$dir/stdin" ]; then
		stdin_file="$dir/stdin"
	fi

	cli_run --stdin "$stdin_file" -- ${args[@]+"${args[@]}"}

	want_status="$(cat "$dir/status" 2>/dev/null || echo 0)"
	cli_expect_status "$want_status" "$name"

	if [ -f "$dir/stdout" ]; then
		cli_expect_stdout_file "$dir/stdout" "$name stdout"
	fi

	if [ -f "$dir/stdout_contains" ]; then
		patterns=()
		while IFS= read -r line || [ -n "$line" ]; do
			patterns+=("$line")
		done <"$dir/stdout_contains"
		cli_expect_stdout_contains ${patterns[@]+"${patterns[@]}"}
	fi

	if [ -f "$dir/stderr_contains" ]; then
		patterns=()
		while IFS= read -r line || [ -n "$line" ]; do
			patterns+=("$line")
		done <"$dir/stderr_contains"
		cli_expect_stderr_contains ${patterns[@]+"${patterns[@]}"}
	fi

	if [ -f "$dir/stderr_exact" ]; then
		cli_expect_stderr_file "$dir/stderr_exact" "$name stderr"
	fi

	if [ -f "$dir/files" ]; then
		while IFS= read -r path || [ -n "$path" ]; do
			[ -n "$path" ] || continue
			cli_expect_file "$path" "$name file $path"
		done <"$dir/files"
	fi
done

test-end
