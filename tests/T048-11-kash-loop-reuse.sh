#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — unchanged loop recipes reuse their outputs.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-11 Kash loop reuse"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	for mode in direct wrapped global; do
		work="$TMPDIR/$backend-$mode"
		mkdir -p "$work"
		printf first >"$work/input"
		if [ "$mode" = direct ]; then
			printf 'SHELL = kash\n./out : ./input\n\tfor item in ["."]\n\t\ttest -d $item\n\tcat ./input > ./out\n' >"$work/Makefile.kmk"
		elif [ "$mode" = wrapped ]; then
			printf 'SHELL = kash\n(helper path) = ((kash """for item in [\".\"]\n    test -d $item\n""") path)\n./out : ./input\n\t(helper "x")\n\tcat ./input > ./out\n' >"$work/Makefile.kmk"
		else
			printf 'SHELL = kash\nhelper = (kash """true""")\n./out : ./input\n\t(helper)\n\tcat ./input > ./out\n' >"$work/Makefile.kmk"
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
	printf 'printf %%s (text (read ./config)) > ./out\n' >"$work/helper.kash"
	printf 'SHELL = kash\nhelper = (kash (text (read ./helper.kash)))\n./out : ./input\n\t(helper)\n' >"$work/Makefile.kmk"
	(cd "$work" && "${runner[@]}" --json ./out >first.json && "${runner[@]}" --json ./out >second.json)
	if [ "$(cat "$work/out")" = first ] && ! grep -q '"type":"process-started"' "$work/second.json"; then
		test-ok "$backend global callable reuses unchanged output"
	else test-fail "$backend global callable reuse"; fi
	printf changed >"$work/config"
	(cd "$work" && "${runner[@]}" --json ./out >changed.json)
	if grep -q '"type":"process-started"' "$work/changed.json" && [ "$(cat "$work/out")" = changed ]; then
		test-ok "$backend global callable tracks body reads"
	else test-fail "$backend global callable body dependency"; fi
	printf 'printf updated > ./out\n' >"$work/helper.kash"
	(cd "$work" && "${runner[@]}" --json ./out >script-changed.json)
	if grep -q '"type":"process-started"' "$work/script-changed.json" && [ "$(cat "$work/out")" = updated ]; then
		test-ok "$backend global callable tracks constructor reads"
	else test-fail "$backend global callable constructor dependency"; fi
	work="$TMPDIR/$backend-closure"
	mkdir -p "$work"
	printf input >"$work/input"
	printf first >"$work/config"
	printf 'SHELL = kash\n(make x) = (kash """printf %%s $x > ./out""")\nhelper = (make (text (read ./config)))\n./out : ./input\n\t(helper)\n' >"$work/Makefile.kmk"
	(cd "$work" && "${runner[@]}" --json ./out >first.json && "${runner[@]}" --json ./out >second.json)
	printf changed >"$work/config"
	(cd "$work" && "${runner[@]}" --json ./out >changed.json)
	if grep -q '"type":"process-started"' "$work/second.json" && [ "$(cat "$work/out")" = changed ]; then
		test-ok "$backend captured callable remains conservative"
	else test-fail "$backend captured callable identity"; fi
done
test-end
