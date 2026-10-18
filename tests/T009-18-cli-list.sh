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
  cli_expect_stdout_contains '  Rules' '  Services' '  Tasks' '  Artifacts' 'test  unknown' './build/app  missing'
  cli_run -- do list -C "$project" first second
  cli_expect_status 2
  cli_expect_stderr_contains 'OPT_VALUE_INVALID'
  cli_run -- do list --help
  cli_expect_status 0
  cli_expect_stdout_contains 'Usage: kame do list' 'typo' 'Inspection never executes recipes'
  cli_expect_no_file "$project/forbidden"
  cli_expect_no_file "$project/build"
  cli_expect_no_file "$project/.kame"

  test-step "$backend shows read-only content/cache status with semantic colors"
  status_project="$TEST_PATH/status-$backend"
  mkdir -p "$status_project"
  printf 'unique-seed-content' >"$status_project/seed"
  cat >"$status_project/Makefile.kmk" <<'KMK'
build : ./result checked
service daemon :
	touch forbidden-service
task checked : ./seed
	@(out! (text (read ./seed)))
task never : ./seed
	touch forbidden-task
task parameterized {mode} :
task defaulted {mode=debug} : ./seed
	@(out! mode)
./result : ./seed
	@(yield! (read ./seed))
./absent : ./seed
	touch forbidden-file
./downstream : ./obj/one.o
	@(yield! (read ./obj/one.o))
./obj/{name}.o : ./src/{name}.c
	@(yield! (read (first inputs)))
./pair ./pair.map : ./seed
	cat ./seed > ./pair
	cp ./pair ./pair.map
KMK
  mkdir -p "$status_project/src"
  printf 'pattern-seed' >"$status_project/src/one.c"
  cli_run -- do list --output text -C "$status_project"
  cli_expect_status 0
  cli_expect_stderr_empty
  cli_expect_stdout_contains 'checked  missing' 'defaulted  missing' 'never  missing' './result  missing' 'parameterized  unknown'
  cli_expect_no_file "$status_project/.kame"

  for target in checked defaulted ./result ./downstream ./pair; do
    cli_run -- -C "$status_project" "$target"
    cli_expect_status 0
  done
  find "$status_project/.kame" -type f -printf '%P %s %T@\n' | sort >"$TEST_PATH/cache-before"
  cli_run -- do list --color always -C "$status_project"
  cli_expect_status 0
  cli_expect_stderr_empty
  cli_expect_stdout_contains $'\e[33mRules\e[0m' $'\e[36mServices\e[0m' $'\e[35mTasks\e[0m' $'\e[34mArtifacts\e[0m' $'\e[32mok\e[0m' $'\e[1;31mmissing\e[0m'
  cli_run -- do list --color never -C "$status_project"
  cli_expect_status 0
  cli_expect_stdout_contains 'checked  ok' 'defaulted  ok' './result  ok' './downstream  ok' './pair  ok' './pair.map  ok'
  if grep -Fq 'unique-seed-content' "$CLI_OUT" || grep -Fq $'\e[' "$CLI_OUT"; then test-fail "$backend listing replayed logs or ignored color=never"; else test-ok "$backend plain status output has no logs or escapes"; fi
  cp -p "$status_project/seed" "$TEST_PATH/stamp"
  printf 'changed-seed-content' >"$status_project/seed"
  touch -r "$TEST_PATH/stamp" "$status_project/seed"
  printf 'changed-pattern-seed' >"$status_project/src/one.c"
  cli_run -- do list --color always -C "$status_project"
  cli_expect_status 0
  cli_expect_stdout_contains $'\e[93mout of date\e[0m'
  cli_run -- do list --output text -C "$status_project"
  cli_expect_status 0
  cli_expect_stdout_contains 'checked  out of date' 'defaulted  out of date' './result  out of date' './absent  missing' './downstream  unknown' './pair  out of date' './pair.map  out of date'
  cli_run -- do list --output text -C "$status_project" downstream
  cli_expect_status 0
  cli_expect_stdout_contains './downstream  unknown'
  cli_expect_file "$status_project/result" 'unique-seed-content'
  find "$status_project/.kame" -type f -printf '%P %s %T@\n' | sort >"$TEST_PATH/cache-after"
  if cmp -s "$TEST_PATH/cache-before" "$TEST_PATH/cache-after"; then test-ok "$backend listing does not write cache records or locks"; else test-fail "$backend listing modified the cache"; fi
  cli_run -- do list --output text --allow-read="$project" -C "$status_project"
  cli_expect_status 0
  cli_expect_stdout_contains 'checked  unknown' './result  unknown'
  for forbidden in service task file; do cli_expect_no_file "$status_project/forbidden-$forbidden"; done
done
if diff -u "$project/native.text" "$project/wasm.text"; then test-ok "human output agrees across hosts"; else test-fail "host presentation differs"; fi
test-end
