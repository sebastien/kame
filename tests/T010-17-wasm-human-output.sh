#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper, stream separation
# Spec: docs/spec/009-cli.md — progress, diagnostics, and summary
# Spec: docs/spec/013-tests.md — T010-17-wasm-human-output
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-17 wasm human build output"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-human"
mkdir -p "$project"

# The summary duration is wall-clock; normalize it before comparing.
normalize() {
	sed -E 's/in [0-9]+\.[0-9]{3}s/in T/'
}

compare() {
	local label="$1"
	shift
	(cd "$project" && "$CLI_BIN" "$@" >"$project/native.out" 2>"$project/native.err") || true
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" "$@" >"$project/wasm.out" 2>"$project/wasm.err") || true
	if normalize <"$project/native.err" >"$project/native.err.n" && normalize <"$project/wasm.err" >"$project/wasm.err.n" && cmp -s "$project/native.err.n" "$project/wasm.err.n" && cmp -s "$project/native.out" "$project/wasm.out"; then
		test-ok "$label"
	else
		test-fail "$label: $(diff "$project/native.err.n" "$project/wasm.err.n" | head -6) $(diff "$project/native.out" "$project/wasm.out" | head -4)"
	fi
}

cat >"$project/Makefile.kmk" <<'EOF'
./out.txt :
	echo recipe-stdout
	echo recipe-stderr 1>&2
	printf x > out.txt
EOF

test-step "a successful primary build matches native progress and streams"
rm -f "$project/out.txt"
compare "success" ./out.txt

test-step "a recipe failure matches native diagnostic and summary"
cat >"$project/Makefile.kmk" <<'EOF'
./bad.txt :
	printf partial > bad.txt
	false
EOF
compare "recipe failure" ./bad.txt

test-step "a missing target matches native failure diagnostic"
cat >"$project/Makefile.kmk" <<'EOF'
./out.txt :
	printf x > out.txt
EOF
compare "missing target" ./nope.txt

test-step "a parse error matches native diagnostic and excerpt"
cat >"$project/Makefile.kmk" <<'EOF'
GREETING = hello
this is not valid
EOF
compare "parse error" ./out.txt

test-end
