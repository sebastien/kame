#!/usr/bin/env bash
# Spec: docs/skills/kame/ref/cli.md — declared target/artifact discovery
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-18 target listing and typo matching"
cli_require_tools
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
native_bin="$CLI_BIN"
wasm_bin="$TEST_PATH/kame-wasm"
printf '#!/usr/bin/env bash\nexec node %q "$@"\n' "$CLI_ROOT/dist/kame.js" >"$wasm_bin"
chmod +x "$wasm_bin"

project="$TEST_PATH/project"
mkdir -p "$project/rules"
cat >"$project/Makefile.kmk" <<'KMK'
ROOT = "./build"
VALUE = 42
(include ./rules/outputs.kmk)
build : unknown-prerequisite
	touch forbidden
builder :
rebuild :
task test {mode} :
service daemon :
./café :
generate extra = [[kind: "task" target: "generated" inputs: [] order-only: [] recipe: ["touch forbidden"]]]
KMK
cat >"$project/rules/outputs.kmk" <<'KMK'
@{ROOT}/{name}.o : ./src/{name}.c
	touch forbidden
./build/app ./build/app.map : ./missing-input
	touch forbidden
KMK

for backend in native wasm; do
  if [ "$backend" = native ]; then CLI_BIN="$native_bin"; else CLI_BIN="$wasm_bin"; fi
  test-step "$backend lists every declared output without needing default or prerequisites"
  cli_run -- do list --json -C "$project"
  cli_expect_status 0
  cli_expect_stderr_empty
  cli_expect_json_query "$CLI_OUT" '. == ["./build/app","./build/app.map","./build/{name}.o","./café","build","builder","daemon","generated","rebuild","test"]' true
  for query in biuld buld buuild builx; do
    cli_run -- do list --json -C "$project" "$query"
    cli_expect_status 0
    cli_expect_json_query "$CLI_OUT" '. == ["build"]' true
  done
  cli_run -- do list --json -C "$project" build
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == ["build","./build/app","./build/app.map","./build/{name}.o","builder","rebuild"]' true
  cli_run -- do list --json -C "$project" apl
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == ["./build/app"]' true
  cli_run -- do list --json -C "$project" cafè
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == ["./café"]' true
  cli_run -- do list --json -C "$project" no-such-target-anywhere
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == []' true
  cli_run -- do list --json -C "$project" -- --help
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == []' true
  cli_run -- do list --json -C "$project" --set 'ROOT="./other"' other
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == ["./other/{name}.o"]' true
  cli_run -- do list --json -c 'only :'
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == ["only"]' true
  cli_run -- do list --json -c 'VALUE = 1'
  cli_expect_status 0
  cli_expect_json_query "$CLI_OUT" '. == []' true
  cli_run -- do list -C "$project" --output text
  cli_expect_status 0
  cli_expect_stderr_empty
  cp "$CLI_OUT" "$project/$backend.text"
  cli_expect_stdout_contains 'list' './build/{name}.o' 'generated'
  cli_run -- do list -C "$project" first second
  cli_expect_status 2
  cli_expect_stderr_contains 'OPT_VALUE_INVALID'
  cli_run -- do list --help
  cli_expect_status 0
  cli_expect_stdout_contains 'Usage: kame do list' 'typo' 'Inspection never executes recipes'
  cli_expect_no_file "$project/forbidden"
  cli_expect_no_file "$project/build"
  cli_expect_no_file "$project/.kame"
done
if diff -u "$project/native.text" "$project/wasm.text"; then test-ok "human output agrees across hosts"; else test-fail "host presentation differs"; fi
test-end
