#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — shared recipe deadlines and failed background work
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-25 Kash recipe deadlines"
cli_build
if [ -n "${KAME_WASM:-}" ]; then wasm_cli="$CLI_ROOT/src/js/kame.js"; else make -C "$CLI_ROOT" dist-wasm >/dev/null; wasm_cli="$CLI_ROOT/dist/kame.js"; fi
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_cli"); fi
	test-step "$backend recipe deadlines and failed background-work cleanup"
	python3 "$CLI_ROOT/tests/recipe-settings.py" "$CLI_ROOT" "$backend" "$CLI_BIN" lifecycle
	python3 "$CLI_ROOT/tests/kash-recipes.py" "$CLI_ROOT" "$backend" "$CLI_BIN" lifecycle
	test-ok "$backend recipe deadlines"
	test-step "$backend mixed-session and expression process deadlines"
	for source in session expression; do
		if [ "$source" = session ]; then
			arguments=(--timeout 250 -l km -c $'task default : second\ntask first\n\tsleep 0.15\ntask second : first\n\tsleep 0.15\n' default)
		else
			arguments=(--allow-run -l expr -c '(run! :timeout 0.05 "sleep" "30")')
		fi
		status=0
		timeout -k 2 15 "${runner[@]}" "do" run "${arguments[@]}" >"$TEST_PATH/out" 2>"$TEST_PATH/err" || status=$?
		if [ "$status" = 1 ] && grep -q RECIPE_TIMEOUT "$TEST_PATH/err" && [ ! -s "$TEST_PATH/out" ]; then test-ok "$backend $source deadline"; else test-fail "$backend $source deadline (status=$status)"; fi
	done
done
test-end
