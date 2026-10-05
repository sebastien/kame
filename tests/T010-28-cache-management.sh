#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — cache inspection and cleanup
# Spec: docs/spec/010-wasm.md — cache host parity
# Spec: docs/spec/013-tests.md — T010-28-cache-management
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-28 cache management"
cd "$CLI_ROOT"

test-step "build native and wasm artifacts"
make dist-wasm >/dev/null
cli_build

prepare_project() {
  local project="$1"
  mkdir -p "$project"
  printf 'task cached :\n\techo run >> runs.log\n\t@(out "cached-output")\n' >"$project/Makefile.kmk"
}
run_native() { (cd "$1" && "$CLI_BIN" cached) >/dev/null 2>&1; }
run_wasm() { (cd "$1" && node "$CLI_ROOT/dist/kame.js" cached) >/dev/null 2>&1; }
cache_command() {
  local host="$1" project="$2" action="$3"
  if [ "$host" = native ]; then
    (cd "$project" && "$CLI_BIN" do cache "$action")
  else
    (cd "$project" && node "$CLI_ROOT/dist/kame.js" do cache "$action")
  fi
}

native="$TMPDIR/cache-manage-native"
wasm="$TMPDIR/cache-manage-wasm"
prepare_project "$native"
prepare_project "$wasm"
run_native "$native"
run_wasm "$wasm"

test-step "list returns JSON records on both hosts"
cache_command native "$native" list >"$native/list.json"
cache_command wasm "$wasm" list >"$wasm/list.json"
if python3 - "$native/list.json" "$wasm/list.json" <<'PY'
import json, sys
for path in sys.argv[1:]:
    rows = json.load(open(path, encoding="utf-8"))
    assert rows, f"no records in {path}"
    assert all(set(row) == {"backend", "key", "bytes"} and row["bytes"] >= 0 for row in rows)
    assert {row["backend"] for row in rows} <= {"tasks", "host", "file-context"}
PY
then test-ok "native and wasm list managed record metadata"; else test-fail "cache list JSON is invalid"; fi

test-step "clean removes files but preserves symlinks"
for project in "$native" "$wasm"; do
  mkdir -p "$project/.kame/cache/tasks"
  printf keep >"$project/marker"
  ln -sf "$project/marker" "$project/.kame/cache/tasks/user-link"
done
cache_command native "$native" clean >"$native/clean.txt"
cache_command wasm "$wasm" clean >"$wasm/clean.txt"
for host_project in "native:$native" "wasm:$wasm"; do
  host="${host_project%%:*}"
  project="${host_project#*:}"
  [ -L "$project/.kame/cache/tasks/user-link" ]
  [ "$(cat "$project/marker")" = keep ]
  [ "$(cache_command "$host" "$project" list)" = '[]' ]
done
test-ok "clean removes regular records without following symlinks"

test-end
