#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — cached tasks
# Spec: docs/spec/010-wasm.md — cache host requests
# Spec: docs/spec/013-tests.md — T010-19-wasm-cache
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-19 wasm cache host requests"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-cache"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF'
task cached :
	echo run >> runs.log
	@(out "cached-output")
EOF

runs() {
	wc -l <"$1/runs.log" 2>/dev/null || echo 0
}

test-step "a cached task replays on the wasm backend"
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >"$project/out1.txt" 2>"$project/err1.txt"
if [ "$(runs "$project")" = 1 ] && grep -q 'cached-output' "$project/out1.txt"; then
	test-ok "first wasm run executes the recipe"
else
	test-fail "first run: runs=$(runs "$project") err=$(cat "$project/err1.txt")"
fi
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >"$project/out2.txt" 2>"$project/err2.txt"
if [ "$(runs "$project")" = 1 ] && cmp -s "$project/out1.txt" "$project/out2.txt"; then
	test-ok "second wasm run is a cache hit with identical output"
else
	test-fail "second run: runs=$(runs "$project")"
fi

test-step "the host cache record persists under the project cache root"
record_count="$(find "$project/.kame/cache/host" -type f 2>/dev/null | wc -l)"
if [ "$record_count" = 1 ]; then
	test-ok "one host cache record was written"
else
	test-fail "host cache records: $record_count"
fi

test-step "the wasm cache matches native"
native="$TMPDIR/wasm-cache-native"
mkdir -p "$native"
cat >"$native/Makefile.kmk" <<'EOF'
task cached :
	echo run >> runs.log
	@(out "cached-output")
EOF
(cd "$native" && "$CLI_BIN" cached) >"$native/out1.txt" 2>/dev/null
(cd "$native" && "$CLI_BIN" cached) >"$native/out2.txt" 2>/dev/null
if [ "$(runs "$native")" = 1 ] && cmp -s "$native/out1.txt" "$project/out1.txt" && cmp -s "$native/out2.txt" "$project/out2.txt"; then
	test-ok "native and wasm agree on cache hits and replay output"
else
	test-fail "native runs=$(runs "$native")"
fi

test-step "deleting the record forces a rerun"
find "$project/.kame/cache/host" -type f -delete
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >/dev/null 2>&1
if [ "$(runs "$project")" = 2 ]; then
	test-ok "a missing cache record reruns the recipe"
else
	test-fail "after delete: runs=$(runs "$project")"
fi

test-step "cache publication replaces record symlinks without following them"
record="$(find "$project/.kame/cache/host" -type f -print -quit)"
printf marker >"$project/marker"
rm -f "$record"
ln -s "$project/marker" "$record"
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >/dev/null 2>&1
if [ "$(cat "$project/marker")" = marker ] && [ ! -L "$record" ] && [ "$(runs "$project")" = 3 ]; then
 test-ok "cache put preserves symlink target and replaces the record"
else
 test-fail "cache publication followed a symlink"
fi
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >/dev/null 2>&1
if [ "$(runs "$project")" = 3 ]; then test-ok "published record is readable on the next invocation"; else test-fail "published record was not complete"; fi
if find "$project" -name '.kame-write-*' -print -quit | grep -q .; then test-fail "cache publication leaked staging"; else test-ok "cache staging was cleaned"; fi

test-end
