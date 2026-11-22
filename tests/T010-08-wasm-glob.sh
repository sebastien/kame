#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/007-library.md — wildcard
# Spec: docs/spec/013-tests.md — T010-08-wasm-glob
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-08 wasm glob service"
cd "$CLI_ROOT"

test-step "build artifacts"
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
cli_build

project="$TMPDIR/wasm-glob"
mkdir -p "$project/src/a" "$project/src/b"
printf x >"$project/a.md"
printf x >"$project/b.md"
printf x >"$project/.hidden.md"
printf x >"$project/c.txt"
printf x >"$project/src/a/one.km"
printf x >"$project/src/two.km"

test-step "wildcard output matches native byte-for-byte"
for expression in '(wildcard ./*.md)' '(wildcard ./src/**/*.km)' '(wildcard ./src/*.km)' '(count (wildcard ./*))'; do
	(cd "$project" && node "$wasm_runner" do run --lang expr --allow-read -c "$expression") >"$project/wasm.out"
	(cd "$project" && "$CLI_BIN" do run --lang expr --allow-read -c "$expression") >"$project/native.out"
	if cmp -s "$project/wasm.out" "$project/native.out"; then
		test-ok "parity: $expression"
	else
		test-fail "parity: $expression (wasm=$(cat "$project/wasm.out") native=$(cat "$project/native.out"))"
	fi
done

test-step "absolute discovery uses root-relative paths and UTF-8 order"
mkdir -p "$project/unicode"
touch "$project/unicode/"$'\U00010000' "$project/unicode/"$'\ue000'
absolute_expression="wildcard $project/unicode/*"
(cd "$project" && "$CLI_BIN" do run --lang expr --allow-read -c "$absolute_expression") >"$project/native.out"
(cd "$project" && node "$wasm_runner" do run --lang expr --allow-read -c "$absolute_expression") >"$project/wasm.out"
expected=$'[./unicode/\ue000, ./unicode/\U00010000]'
if cmp -s "$project/native.out" "$project/wasm.out" && [ "$(cat "$project/wasm.out")" = "$expected" ]; then
	test-ok "absolute discovery is relative to the root and sorted by UTF-8 bytes"
else
	test-fail "absolute discovery parity: native=$(cat "$project/native.out"), wasm=$(cat "$project/wasm.out")"
fi

test-step "recursive discovery does not follow encountered directory symlinks"
mkdir -p "$project/real/deep" "$project/links"
touch "$project/real/deep/file.js"
ln -s ../real "$project/links/alias"
ln -s .. "$project/links/cycle"
ln -s ../absent "$project/links/dangling"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	for sample in \
		'(wildcard ./links/alias/**/*.js)|[./links/alias/deep/file.js]' \
		'(wildcard ./links/alias/*/*.js)|[./links/alias/deep/file.js]' \
		'(wildcard ./links/**/*.js)|[]'; do
		(cd "$project" && "${runner[@]}" do run --lang expr --allow-read -c "${sample%|*}") >"$project/value"
		if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend symlink glob result: $(cat "$project/value")"; fi
	done
done

test-step "command globs prune hidden traversal and match explicit dot components"
mkdir -p "$project/glob/visible/poison" "$project/glob/visible/.blocked" "$project/glob/.explicit/.nested"
touch "$project/glob/visible/poison/hit.txt" "$project/glob/.explicit/shown.txt" "$project/glob/.explicit/.nested/shown.txt"
chmod 000 "$project/glob/visible/.blocked"
command='printf "<%s>\n" glob/**/poison/*.txt glob/**/.exp*/**/shown*.txt glob/visible/**'
expected=$'<./glob/visible/poison/hit.txt>\n<./glob/.explicit/shown.txt>\n<./glob/visible/poison>\n<./glob/visible/poison/hit.txt>'
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	if value=$(cd "$project" && "${runner[@]}" do run --lang kash --allow-read --allow-run -c "$command") && [ "$value" = "$expected" ]; then
		test-ok "$backend prunes hidden subtrees and matches explicit dots"
	else
		test-fail "$backend command glob: $value"
	fi
done
chmod 700 "$project/glob/visible/.blocked"

test-step "command globs sort absolute argv after root-relative matches are normalized"
mkdir -p "$project/sub" "$TMPDIR/a-outside"
touch "$project/hit-two" "$TMPDIR/a-outside/hit-one"
expected=$(printf '<%s>\n' "$TMPDIR/a-outside/hit-one" "$project/hit-two")
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	if value=$(cd "$project" && "${runner[@]}" do run --lang kash --allow-read --allow-run -c 'cwd: ./sub printf "<%s>\n" ../../*/hit-*') && [ "$value" = "$expected" ]; then
		test-ok "$backend sorts the final argument spellings"
	else
		test-fail "$backend command glob ordering: $value"
	fi
done

test-step "custom callbacks keep kind 6 and may return ordinary candidates"
if KAME_RUNNER="$wasm_runner" node --input-type=module - <<'NODE'; then test-ok "command glob callback compatibility"; else test-fail "command glob callback compatibility"; fi
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const { Kame } = await import(pathToFileURL(process.env.KAME_RUNNER));
const kame = await Kame.create();
try {
  const program = await kame.compile('');
  const modes = [];
  const options = {
    grants: [{ capability: 'read' }, { capability: 'run' }],
    async hostRequest(request) {
      if (request.kind === 6) {
        modes.push(request.commandGlob);
        return { type: 'json', value: ['./.hidden', './visible'] };
      }
      if (request.kind === 18) return { type: 'text', value: request.payload };
      assert.equal(request.kind, 13);
      assert.deepEqual(JSON.parse(request.payload), ['/bin/printf', '%s', './visible']);
      return { type: 'failure', code: 'EXEC_CANCELLED', message: 'argv observed' };
    },
  };
  assert.equal(await program.evaluate('(wildcard ./*)', options), '[./.hidden, ./visible]');
  await assert.rejects(program.evaluate('$(/bin/printf "%s" ./*)', options), /argv observed/);
  assert.deepEqual(modes, [undefined, true]);
} finally {
  await kame.dispose();
}
NODE

test-step "wildcard needs a read grant"
set +e
(cd "$project" && node "$wasm_runner" do run --lang expr -c '(wildcard ./*.md)') >"$project/out.txt" 2>"$project/err.txt"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'CAP_DENIED' "$project/err.txt" && grep -q -- '--allow-read=ROOT' "$project/err.txt"; then
	test-ok "wildcard without --allow-read is denied"
else
	test-fail "wildcard denial: status=$status err=$(cat "$project/err.txt")"
fi

test-end
