#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — self-build inputs and generated metadata
# Spec: docs/spec/013-tests.md — T015-03-dist-self-build
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-03 self-build incremental"

test-step "toolchain and existing CLI"
cli_require_tools
if [ ! -x "$CLI_BIN" ]; then
	test-fail "CLI binary is missing: $CLI_BIN"
fi

test-step "version metadata is stable across invocations"
version_file="$CLI_ROOT/src/go/kame/cmd/kame/version_generated.go"
# Prebuilt suites may predate the current revision; normalize before comparing.
"$CLI_ROOT/src/sh/generate-version.sh"
before="$(sha256sum "$version_file")"
"$CLI_ROOT/src/sh/generate-version.sh"
"$CLI_ROOT/src/sh/generate-version.sh"
test-step "concurrent metadata generation uses independent temporary files"
pids=()
for _attempt in {1..8}; do
	"$CLI_ROOT/src/sh/generate-version.sh" &
	pids+=("$!")
done
for pid in "${pids[@]}"; do
	if wait "$pid"; then test-ok "concurrent version generation completed"; else test-fail "concurrent version generation failed"; fi
done
after="$(sha256sum "$version_file")"
if [ "$before" = "$after" ]; then
	test-ok "version metadata did not change"
else
	test-fail "version metadata changed: $before -> $after"
fi

test-step "second build invocation skips generated version source"
self_project="$TMPDIR/self-build"
mkdir -p "$self_project/src/sh" "$self_project/src/go/kame/cmd/kame"
cp "$CLI_ROOT/VERSION" "$self_project/VERSION"
cp -p "$CLI_ROOT/src/sh/generate-version.sh" "$self_project/src/sh/generate-version.sh"
cp "$CLI_ROOT/src/go/kame/cmd/kame/version_generated.go" "$self_project/src/go/kame/cmd/kame/version_generated.go"
cat >"$self_project/Makefile.kmk" <<'KMK'
./src/go/kame/cmd/kame/version_generated.go : ./VERSION ./src/sh/generate-version.sh
	./src/sh/generate-version.sh
KMK
cli_run --dir "$self_project" --env "HOME=$HOME" --env TMPDIR=/tmp -- --force --json ./src/go/kame/cmd/kame/version_generated.go
cli_expect_status 0
if ! grep -q '"type":"process-started"' "$CLI_OUT"; then
	test-fail "forced version generation did not run: $(cat "$CLI_OUT")"
fi

cli_run --dir "$self_project" --env "HOME=$HOME" --env TMPDIR=/tmp -- --json ./src/go/kame/cmd/kame/version_generated.go
cli_expect_status 0
if grep -q '"type":"process-started"' "$CLI_OUT"; then
	test-fail "fresh version source ran again: $(cat "$CLI_OUT")"
else
	test-ok "fresh version source was skipped"
fi

test-step "self-build source prerequisites are declared"
cli_run --dir "$CLI_ROOT" -- do inputs --json ./build/kame.debug
cli_expect_status 0
cli_expect_json_query "$CLI_OUT" '[.resources[] | select(.key.kind == "file") | .display] | contains([
  "./src/go/kame/cmd/kame/main.go",
  "./src/go/kame/host/posix/posix.go",
  "./src/go/kame/host/posix/topology.go",
  "./src/go/kame/cmd/kame/version_generated.go",
  "./VERSION",
  "./src/sh/generate-version.sh"
])' true

test-end
