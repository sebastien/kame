#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/007-library.md — wildcard
# Spec: docs/spec/013-tests.md — T010-08-wasm-glob
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-08 wasm glob service"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-glob"
mkdir -p "$project/src/a" "$project/src/b"
printf x >"$project/a.md"
printf x >"$project/b.md"
printf x >"$project/c.txt"
printf x >"$project/src/a/one.km"
printf x >"$project/src/two.km"

test-step "wildcard output matches native byte-for-byte"
for expression in '(wildcard "*.md")' '(wildcard "src/**/*.km")' '(wildcard "src/*.km")' '(count (wildcard "*"))'; do
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" do run --lang expr --allow-read -c "$expression") >"$project/wasm.out"
	(cd "$project" && "$CLI_BIN" do run --lang expr --allow-read -c "$expression") >"$project/native.out"
	if cmp -s "$project/wasm.out" "$project/native.out"; then
		test-ok "parity: $expression"
	else
		test-fail "parity: $expression (wasm=$(cat "$project/wasm.out") native=$(cat "$project/native.out"))"
	fi
done

test-step "wildcard needs a read grant"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" do run --lang expr -c '(wildcard "*.md")') >"$project/out.txt" 2>"$project/err.txt"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'CAP_DENIED' "$project/err.txt" && grep -q -- '--allow-read=ROOT' "$project/err.txt"; then
	test-ok "wildcard without --allow-read is denied"
else
	test-fail "wildcard denial: status=$status err=$(cat "$project/err.txt")"
fi

test-end
