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
test-end
