#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — tool resolution
# Spec: docs/spec/009-cli.md — Tool overrides
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-11 shared build tool resolution"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/tool project"
mkdir -p "$project"
printf '#!/bin/sh\nprintf tool-a\n' >"$project/tool-a"
printf '#!/bin/sh\nprintf tool-b\n' >"$project/tool-b"
chmod +x "$project/tool-a" "$project/tool-b"
mkdir -p "$project/startup tools" "$project/scoped tools"
printf '#!/bin/sh\nprintf tool-startup\n' > "$project/startup tools/kame-test-compiler"
printf '#!/bin/sh\nprintf tool-scoped\n' > "$project/scoped tools/kame-test-compiler"
chmod +x "$project/startup tools/kame-test-compiler" "$project/scoped tools/kame-test-compiler"
cat >"$project/Makefile.kmk" <<'KMK'
compiler = (tool "kame-test-compiler")
computed = "kame-test-compiler"
default :
	"@(compiler)"
	"@(tool computed)"
scoped : ; env "PATH=./scoped tools"
	"@(compiler)"
	"@(tool computed)"
	"@(x/kame-test-compiler)"
KMK
for backend in native wasm; do
 test-step "$backend tool resolution and overrides"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 "${runner[@]}" -C "$project" --tool kame-test-compiler=./tool-a default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = tool-atool-a ]; then test-ok "$backend literal and computed tool reuse"; else test-fail "$backend tool recipe"; fi
  "${runner[@]}" do plan --json -C "$project" --tool kame-test-compiler=./tool-a default >"$project/plan" 2>"$project/error"
 if python3 - "$project/plan" "$project/tool-a" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['tools']['kame-test-compiler'] == sys.argv[2]
PY
 then test-ok "$backend tool path in plan"; else test-fail "$backend tool plan"; fi
 "${runner[@]}" -C "$project" --tool kame-test-compiler=./tool-a --tool=kame-test-compiler=./tool-b default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = tool-btool-b ]; then test-ok "$backend last tool override wins"; else test-fail "$backend tool override precedence"; fi
 status=0
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q TOOL_MISSING "$project/error" && [ ! -s "$project/value" ]; then test-ok "$backend missing tool fails before process"; else test-fail "$backend missing tool"; fi
 "${runner[@]}" do run --allow-run -l km -c 'selected = "kame-test-compiler"' -c '(tool selected)' --tool "kame-test-compiler=$project/tool-a" >"$project/value" 2>"$project/error"
 if python3 - "$project/value" "$project/tool-a" <<'PY'
import json,sys
assert json.load(open(sys.argv[1])) == sys.argv[2]
PY
 then test-ok "$backend computed tool in unified session"; else test-fail "$backend session tool"; fi
 test-step "$backend preserves startup tool policy under target PATH overrides"
 PATH="$project/startup tools:$PATH" "${runner[@]}" -C "$project" scoped > "$project/value" 2> "$project/error"
 if [ "$(cat "$project/value")" = tool-startuptool-startuptool-startup ]; then test-ok "$backend lazy, computed and authored tools use startup PATH"; else test-fail "$backend target PATH changed declared tool policy"; fi
 PATH="$project/startup tools:$PATH" "${runner[@]}" -C "$project" --tool kame-test-compiler=./tool-b scoped > "$project/value" 2> "$project/error"
 if [ "$(cat "$project/value")" = tool-btool-btool-b ]; then test-ok "$backend explicit tool overrides win under target PATH"; else test-fail "$backend scoped tool override"; fi
done
test-end
