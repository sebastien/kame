#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — Test helper lifecycle and isolation
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T013-06 test helper regressions"

# Nested helpers and harnesses use only this scratch tree, including HOME/logs.
scratch="$TEST_PATH"
mkdir -p "$scratch/home/Desktop" "$scratch/runner/tests"
printf 'valuable-a\n' >"$scratch/home/Desktop/a.txt"
printf 'valuable-b\n' >"$scratch/home/Desktop/b.txt"
cp "$CLI_ROOT/tests/"{lib-testing.sh,lib-cli.sh,harness.sh} "$scratch/runner/tests/"
cat >"$scratch/fixture.sh" <<'SH'
#!/usr/bin/env bash
source "$HELPERS/lib-testing.sh"
source "$HELPERS/lib-cli.sh"
case "$1" in silent-*) exec 2>/dev/null ;; esac
if [ "$2" = init ]; then test-init fixture; else test-start fixture; fi
printf '%s\n' "$TEST_PATH" >"$CASE_DIR/path"
# Keep the append-only rows outside the cleaned child directory for inspection.
TEST_RESULTS="$CASE_DIR/results"
: >"$TEST_RESULTS"
set +e
case "$1" in
fatal|abort)
  test-ok prerequisite
  TEST_EXPECT_FAILURE=yes
  if test-"$1" broken; then :; fi
  touch "$CASE_DIR/continued"
  ;;
silent-fatal|silent-abort)
  test-ok prerequisite
  test-"${1#silent-}" broken 2>/dev/null
  touch "$CASE_DIR/continued"
  ;;
success) (test-ok nested); test-ok parent ;;
failure) (test-fail nested); test-ok parent ;;
diff) if test-expect actual expected; then :; fi ;;
json-valid)
  printf 'false\nnull\n{}\n0\n"text"\n\n' >values
  cli_expect_jsonl values
  ;;
json-empty) : >values; cli_expect_jsonl values ;;
json-multiple) printf '{} {}\n' >values; cli_expect_jsonl values ;;
json-malformed) printf '{\n' >values; cli_expect_jsonl values ;;
esac
test-end
exit $?
SH

test-step "fatal/abort stop even in conditionals, with errexit off and silent stderr"
for mode in fatal abort silent-fatal silent-abort success failure diff json-valid json-empty json-multiple json-malformed; do
  for lifecycle in start init; do
    case_dir="$scratch/$mode-$lifecycle"
    mkdir -p "$case_dir"
    status=0
    (cd "$scratch" && env HOME="$scratch/home" NOCOLOR=1 \
      HELPERS="$scratch/runner/tests" CASE_DIR="$case_dir" \
      timeout --kill-after=2 10 bash "$scratch/fixture.sh" "$mode" "$lifecycle") \
      >"$case_dir/out" 2>"$case_dir/err" || status=$?
    case "$mode" in
      success|json-valid|json-empty) want_status=0; want_ok=1; want_fail=0 ;;
      *) want_status=1; want_ok=1; want_fail=1 ;;
    esac
    case "$mode" in
      success) want_ok=2 ;;
      diff|json-multiple|json-malformed) want_status=2; want_ok=0 ;;
    esac
    test-expect "$status" "$want_status" "$mode/$lifecycle exit status"
    test-expect "$(grep -c '^OK' "$case_dir/results" || true)" "$want_ok" "$mode/$lifecycle OK rows"
    test-expect "$(grep -c '^FAIL' "$case_dir/results" || true)" "$want_fail" "$mode/$lifecycle FAIL rows"
    cli_expect_no_file "$case_dir/continued"
    cli_expect_no_file "$(cat "$case_dir/path")"
    case "$mode" in
      silent-*) test-empty "$case_dir/err" "silent fatal/abort has no report" ;;
      success|json-valid|json-empty) test-contains "$case_dir/err" 'EOK' && test-ok "success summary" ;;
      *) test-contains "$case_dir/err" 'EFAIL' && test-ok "failure summary" ;;
    esac
  done
done

test-step "signals exit helpers instead of returning to deleted scratch directories"
for lifecycle in start init; do
  for signal in INT TERM; do
    case_dir="$scratch/signal-$lifecycle-$signal"
    mkdir -p "$case_dir"
    status=0
    # shellcheck disable=SC2016 # The child expands these variables.
    (cd "$scratch" && env HELPERS="$scratch/runner/tests" CASE_DIR="$case_dir" \
      bash -c 'source "$HELPERS/lib-testing.sh"; test-"$1" cancellation; printf "%s\n" "$TEST_PATH" >"$CASE_DIR/path"; kill -s "$2" "$$"; touch "$CASE_DIR/continued"' bash "$lifecycle" "$signal") \
      >"$case_dir/out" 2>"$case_dir/err" || status=$?
    want_status=130
    [ "$signal" != TERM ] || want_status=143
    test-expect "$status" "$want_status" "$lifecycle/$signal exit status"
    cli_expect_no_file "$case_dir/continued"
    cli_expect_no_file "$(cat "$case_dir/path")"
    if grep -Eq 'EOK|EFAIL|Unmanaged error' "$case_dir/err"; then
      test-fail "cancellation printed a normal result"
    else
      test-ok "cancellation has no misleading result"
    fi
  done
done

test-step "failed diffs do not touch Desktop files"
cli_expect_file "$scratch/home/Desktop/a.txt" $'valuable-a\n'
cli_expect_file "$scratch/home/Desktop/b.txt" $'valuable-b\n'
test-expect "$(find "$scratch/home/Desktop" -type f | wc -l)" 2 "no extra Desktop files"

test-step "compact and verbose harnesses use Bash, not login SHELL"
cat >"$scratch/runner/tests/bash-fixture.sh" <<'SH'
#!/usr/bin/env bash
values=(bash syntax)
[[ "${values[0]}" = bash ]] || exit 1
printf 'ran\n' >"$HOME/harness-ran"
SH
for verbosity in -q -v; do
  for shell_env in SHELL=/bin/sh unset; do
    rm -f "$scratch/home/harness-ran"
    status=0
    env_args=(env)
    if [ "$shell_env" = unset ]; then env_args+=(-u SHELL); else env_args+=("$shell_env"); fi
    (cd "$scratch/runner" && "${env_args[@]}" HOME="$scratch/home" NOCOLOR=1 \
      timeout --kill-after=2 10 bash tests/harness.sh "$verbosity" tests/bash-fixture.sh) \
      >"$scratch/harness.out" 2>"$scratch/harness.err" || status=$?
    test-expect "$status" 0 "$verbosity/$shell_env harness status"
    cli_expect_file "$scratch/home/harness-ran" $'ran\n'
    test-contains "$scratch/harness.out" EOK && test-ok "harness success summary"
  done
done

test-step "harness caches runtimes and reports a remaining-time estimate"
runtime_cache="$scratch/runner/build/tests/runtime-cache"
mkdir -p "$runtime_cache"
printf '17\n' >"$runtime_cache/bash-fixture.sh.seconds"
previous_cache="$TEST_RUNTIME_CACHE_DIR"
TEST_RUNTIME_CACHE_DIR="$runtime_cache"
test-expect "$(test_runtime_eta tests/new-fixture.sh)" '~17s (0/1 cached)' "uncached runtime uses cached mean"
TEST_RUNTIME_CACHE_DIR="$previous_cache"
status=0
(cd "$scratch/runner" && env HOME="$scratch/home" NOCOLOR=1 \
  TEST_RUNTIME_CACHE_DIR="$runtime_cache" timeout --kill-after=2 10 \
  bash tests/harness.sh -q -j 1 tests/bash-fixture.sh) \
  >"$scratch/harness.out" 2>"$scratch/harness.err" || status=$?
test-expect "$status" 0 "cached ETA harness status"
test-contains "$scratch/harness.err" 'ETA: ~17s (1/1 cached)' && test-ok "cached runtime appears in ETA"
if IFS= read -r recorded <"$runtime_cache/bash-fixture.sh.seconds" && [[ "$recorded" =~ ^[0-9]+$ ]]; then
  test-ok "harness records the latest runtime"
else
  test-fail "harness records the latest runtime"
fi
test-end
