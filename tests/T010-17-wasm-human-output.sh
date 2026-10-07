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

# Elapsed durations are host-dependent; normalize them before comparing.
normalize() {
	# Native and forwarded caches establish different miss facts.
	sed -E 's/[0-9]+ms/Tms/g; s/saved record (unavailable|missing)/saved record not reusable/'
}

compare() {
	local label="$1"
	local native_status=0 wasm_status=0
	shift
	# Each backend starts without the output left by the other backend.
	rm -f "$project/out.txt" "$project/bad.txt"
	(cd "$project" && "$CLI_BIN" --output text --color never "$@" >"$project/native.out" 2>"$project/native.err") || native_status=$?
	rm -f "$project/out.txt" "$project/bad.txt"
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" --output text --color never "$@" >"$project/wasm.out" 2>"$project/wasm.err") || wasm_status=$?
	normalize <"$project/native.err" >"$project/native.err.n"
	normalize <"$project/wasm.err" >"$project/wasm.err.n"
	if [ "$native_status" = "$wasm_status" ] && cmp -s "$project/native.err.n" "$project/wasm.err.n" && cmp -s "$project/native.out" "$project/wasm.out"; then
		test-ok "$label"
	else
		test-fail "$label (native=$native_status wasm=$wasm_status): $(diff "$project/native.err.n" "$project/wasm.err.n" | head -6) $(diff "$project/native.out" "$project/wasm.out" | head -4)"
	fi
	if { [ "$label" = success ] && [ "$native_status" = 0 ]; } || { [ "$label" != success ] && [ "$native_status" != 0 ]; }; then test-ok "$label exit status"; else test-fail "$label unexpected exit status $native_status"; fi
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
if [ "$(cat "$project/wasm.out")" = recipe-stdout ] && grep -qx recipe-stderr "$project/wasm.err" && grep -qx 'started \[./out.txt\]' "$project/wasm.err" && grep -qx 'done \[./out.txt\] complete' "$project/wasm.err" && grep -q '^done build · .* targets complete · 0 failed · 0 cancelled · [0-9]*ms$' "$project/wasm.err" && ! grep -q $'\033' "$project/wasm.err"; then test-ok "text lifecycle, summary and separate recipe streams"; else test-fail "text lifecycle or stream separation"; fi

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

test-step "operand diagnostics after multibyte text retain native columns"
compare "Unicode operand failure" -l kmk -c $'root :\n\t@(nop "界e\u0301") @(first 1)\n' root

test-end
