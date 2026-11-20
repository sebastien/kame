#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — unchanged loop recipes reuse their outputs.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-11 Kash loop reuse"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make -C "$CLI_ROOT" dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	for mode in direct wrapped global; do
		work="$TMPDIR/$backend-$mode"
		mkdir -p "$work"
		printf first >"$work/input"
		if [ "$mode" = direct ]; then
			printf 'rule ./out : ./input\n\tfor item in ["."]\n\t\ttest -d $item\n\tcat ./input > ./out\n' >"$work/Makefile.km"
		elif [ "$mode" = wrapped ]; then
			printf 'def helper path\n\tfor item in [path]\n\t\ttest -d $item\nrule ./out : ./input\n\thelper .\n\tcat ./input > ./out\n' >"$work/Makefile.km"
		else
			printf 'def helper ()\n\ttrue\nrule ./out : ./input\n\thelper\n\tcat ./input > ./out\n' >"$work/Makefile.km"
		fi
		(cd "$work" && "${runner[@]}" --json ./out >first.json && "${runner[@]}" --json ./out >second.json)
		if grep -q '"type":"process-started"' "$work/first.json" && ! grep -q '"type":"process-started"' "$work/second.json"; then
			test-ok "$backend $mode loop reuses unchanged output"
		else test-fail "$backend $mode loop reuse"; fi
		printf changed >"$work/input"
		(cd "$work" && "${runner[@]}" --json ./out >changed.json)
		if grep -q '"type":"process-started"' "$work/changed.json" && [ "$(cat "$work/out")" = changed ]; then
			test-ok "$backend $mode loop invalidates on input change"
		else test-fail "$backend $mode loop invalidation"; fi
	done
	work="$TMPDIR/$backend-global"
	mkdir -p "$work"
	printf input >"$work/input"
	printf first >"$work/config"
	printf 'def helper ()\n\tprintf %%s (text (read ./config)) > ./out\n' >"$work/helper.km"
	printf 'include ./helper.km\nrule ./out : ./input\n\thelper\n' >"$work/Makefile.km"
	(cd "$work" && "${runner[@]}" --json ./out >first.json && "${runner[@]}" --json ./out >second.json)
	if [ "$(cat "$work/out")" = first ] && ! grep -q '"type":"process-started"' "$work/second.json"; then
		test-ok "$backend global callable reuses unchanged output"
	else test-fail "$backend global callable reuse"; fi
	printf changed >"$work/config"
	(cd "$work" && "${runner[@]}" --json ./out >changed.json)
	if grep -q '"type":"process-started"' "$work/changed.json" && [ "$(cat "$work/out")" = changed ]; then
		test-ok "$backend global callable tracks body reads"
	else test-fail "$backend global callable body dependency"; fi
	printf 'def helper ()\n\tprintf updated > ./out\n' >"$work/helper.km"
	(cd "$work" && "${runner[@]}" --json ./out >script-changed.json)
	if grep -q '"type":"process-started"' "$work/script-changed.json" && [ "$(cat "$work/out")" = updated ]; then
		test-ok "$backend global callable tracks included source changes"
	else test-fail "$backend global callable included source dependency"; fi
	work="$TMPDIR/$backend-closure"
	mkdir -p "$work"
	printf input >"$work/input"
	printf first >"$work/config"
	printf 'def make value\n\tdef produce ()\n\t\tprintf %%s $value > ./out\n\treturn produce\ndef use callback\n\t(callback ())\nrule ./out : ./input\n\t(use (make (text (read ./config))))\n' >"$work/Makefile.km"
	(cd "$work" && "${runner[@]}" --json ./out >first.json && "${runner[@]}" --json ./out >second.json)
	if [ "$(cat "$work/out")" = first ] && grep -q '"type":"process-started"' "$work/first.json" &&
		! grep -q '"type":"process-started"' "$work/second.json"; then
		test-ok "$backend captured configuration reuses unchanged output"
	else test-fail "$backend captured callable reuse"; fi
	printf changed >"$work/config"
	(cd "$work" && "${runner[@]}" --json ./out >changed.json)
	if grep -q '"type":"process-started"' "$work/changed.json" && [ "$(cat "$work/out")" = changed ]; then
		test-ok "$backend captured configuration invalidates changed output"
	else test-fail "$backend captured callable identity"; fi
done
test-end
