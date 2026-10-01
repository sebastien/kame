#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-08 runner file includes"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/includes"
mkdir -p "$work/nested" "$work/cwd"
cat >"$work/main.km" <<'KM'
(out "界")
include ./nested/child.km
(out "after")
(cat name "!")
KM
cat >"$work/nested/child.km" <<'KM'
include ./values.km
(out "child")
KM
printf 'name = "Ada"\n' >"$work/nested/values.km"
cat >"$work/rules.kmk" <<'KMK'
include ./nested/rules.kmk
build : child
	printf main
KMK
cat >"$work/nested/rules.kmk" <<'KMK'
child :
	printf child
KMK
cat >"$work/cycle.km" <<'KM'
(out "must-not-run")
include ./nested/cycle.km
KM
printf 'include ../cycle.km\n' >"$work/nested/cycle.km"
printf 'include ./absent.km\n' >"$work/missing.km"
printf 'include ./nested/invalid.km\n' >"$work/invalid.km"
printf 'name = [\n' >"$work/nested/invalid.km"
printf 'include ./empty.km\ninclude ./empty.km\n42\n' >"$work/repeated.km"
: >"$work/empty.km"
printf 'include "%s"\nname\n' "$work/nested/values.km" >"$work/absolute.km"

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend: $*"; else test-fail "$backend include result (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code before effects"; else test-fail "$backend include rejection (status $status)"; cat "$work/err"; fi
	}
	test-step "$backend includes preserve scope, order, byte offsets and entry selection"
	check '界childafter"Ada!"' do run "$work/main.km"
	check '"Ada"' do run "$work/main.km" name
	check '界childafter"Ada!"' do run -C "$work/cwd" "$work/main.km"
	check '42' do run "$work/repeated.km"
	check '"Ada"' do run "$work/absolute.km"
	check 'childmain"Ada"' do run "$work/rules.kmk" build -c 'name = "Ada"' -c 'name'
	check 'childmain' do run "$work/rules.kmk" build
	reject DEP_CYCLE do run "$work/cycle.km"
	reject FS_ERR do run -c '(out "must-not-run")' "$work/missing.km"
	reject PARSE_ERR do run -c '(out "must-not-run")' "$work/invalid.km"
done
test-end
