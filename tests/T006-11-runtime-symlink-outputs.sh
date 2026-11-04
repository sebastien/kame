#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — symlink output identity
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-11 symlink outputs"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/src/py" "$work/deps/hjson/hjson" "$work/deps/toon/src/toon_format"
	printf seed >"$work/seed"
	printf first >"$work/payload"
	cat >"$work/Makefile.kmk" <<'KMK'
prep : ./src/py/hjson ./src/py/toon_format
./src/py/hjson : ./deps/hjson
	ln -sfrTn @</hjson @>
./src/py/toon_format : ./deps/toon
	ln -sfrTn @</src/toon_format @>
./link : ./seed
	printf x >> runs
	ln -sfn deps/hjson/hjson @>
./dangling : ./seed
	ln -sfn absent @>
./loop : ./seed
	ln -sfn loop @>
./file-link : ./seed
	ln -sfn payload @>
./copy : ./file-link ; [shell: kame]
	(read @<)
KMK
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend prep publishes links to directories"
	"${runner[@]}" -C "$work" prep
	[ "$(readlink "$work/src/py/hjson")" = ../../deps/hjson/hjson ]
	[ "$(readlink "$work/src/py/toon_format")" = ../../deps/toon/src/toon_format ]
	test-ok "$backend directory symlink outputs"
	test-step "$backend reuses unchanged links and repairs changed entries"
	"${runner[@]}" -C "$work" ./link ./dangling ./loop
	"${runner[@]}" -C "$work" ./link ./dangling ./loop
	[ "$(cat "$work/runs")" = x ]
	ln -sfn deps/toon/src/toon_format "$work/link"
	"${runner[@]}" -C "$work" ./link
	[ "$(cat "$work/runs")" = xx ]
	[ "$(readlink "$work/link")" = deps/hjson/hjson ]
	rm "$work/link"
	"${runner[@]}" -C "$work" ./link
	[ "$(cat "$work/runs")" = xxx ]
	rm "$work/link"
	printf deps/hjson/hjson >"$work/link"
	"${runner[@]}" -C "$work" ./link
	[ "$(cat "$work/runs")" = xxxx ]
	[ -L "$work/link" ]
	test-ok "$backend symlink freshness, dangling links and loops"
	test-step "$backend reads through generated links still track referent bytes"
	"${runner[@]}" -C "$work" ./copy
	[ "$(cat "$work/copy")" = first ]
	"${runner[@]}" -C "$work" ./copy
	printf second >"$work/payload"
	"${runner[@]}" -C "$work" ./copy
	[ "$(cat "$work/copy")" = second ]
	test-ok "$backend followed content invalidation"
done
test-end
