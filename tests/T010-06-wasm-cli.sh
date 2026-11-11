#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/013-tests.md — T010-06-wasm-cli
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-06 wasm JavaScript CLI"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

run() {
	set +e
	node "$CLI_ROOT/dist/kame.js" "$@" >"$work/out" 2>"$work/err"
	status=$?
	set -e
}

test-step "-C resolves relative explicit files in both option orders"
mkdir -p "$work/project"
cat > "$work/project/Makefile.kmk" <<'EOF'
task default : ; [shell: kash]
	(out! "directory-ok\n")
EOF
for args in first last absolute discover plan; do
	case "$args" in
	first) invocation=(-C "$work/project" -f Makefile.kmk default) ;;
	last) invocation=(-f Makefile.kmk -C "$work/project" default) ;;
	absolute) invocation=(-C "$work/project" -f "$work/project/Makefile.kmk" default) ;;
	discover) invocation=(-C "$work/project" default) ;;
	plan) invocation=(do plan -C "$work/project" -f Makefile.kmk default) ;;
	esac
	run "${invocation[@]}"
	wasm_status=$status
	cp "$work/out" "$work/wasm-directory.out"
	set +e
	"$CLI_BIN" "${invocation[@]}" >"$work/native-directory.out" 2>"$work/native-directory.err"
	native_status=$?
	set -e
	if [ "$wasm_status" = 0 ] && [ "$native_status" = 0 ] && cmp -s "$work/wasm-directory.out" "$work/native-directory.out"; then
		test-ok "directory source parity: $args"
	else
		test-fail "directory source parity: $args wasm=$wasm_status native=$native_status"
	fi
done

test-step "misplaced typed inspection options cannot run build targets"
cat >"$work/project/Makefile.kmk" <<'EOF'
NAME = 0
do :
	touch ./misexecuted
plan :
	touch ./misexecuted
EOF
for option in '--set NAME=1 do plan' '--set=NAME=1 do plan' '--set NAME=1 --set=NAME=2 do plan' '--set NAME=1 do -j 2 plan' 'do plan --set NAME=1' 'do plan --set NAME=1 --'; do
	read -r -a set_args <<<"$option"
	for runner in wasm native; do
		rm -f "$work/project/misexecuted"
		if [ "$runner" = wasm ]; then run -C "$work/project" "${set_args[@]}"; else
			set +e
			"$CLI_BIN" -C "$work/project" "${set_args[@]}" >"$work/out" 2>"$work/err"
			status=$?
			set -e
		fi
		if [ "$status" = 2 ] && grep -q OPT_CONFLICT "$work/err" && [ ! -e "$work/project/misexecuted" ]; then
			test-ok "$runner rejects $option before recipes"
		else
			test-fail "$runner accepted $option or ran a target (status=$status)"
		fi
	done
done

for runner in wasm native; do
	for mode in inspection literal; do
		rm -f "$work/project/misexecuted"
		if [ "$mode" = inspection ]; then
			invocation=(do plan -C "$work/project" --set NAME=1 do plan)
		else
			invocation=(-C "$work/project" --set NAME=1 -- do plan)
		fi
		if [ "$runner" = wasm ]; then run "${invocation[@]}"; else
			set +e
			"$CLI_BIN" "${invocation[@]}" >"$work/out" 2>"$work/err"
			status=$?
			set -e
		fi
		if [ "$status" = 0 ] && { { [ "$mode" = inspection ] && [ ! -e "$work/project/misexecuted" ]; } || { [ "$mode" = literal ] && [ -e "$work/project/misexecuted" ]; }; }; then
			test-ok "$runner preserves $mode target selection"
		else
			test-fail "$runner changed $mode target selection (status=$status)"
		fi
	done
done

test-step "version does not require the module"
for flag in --version -V; do
	KAME_WASM="$work/missing.wasm" run "$flag"
	if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "kame $(cat "$CLI_ROOT/VERSION") (runtime=WASM)" ] && [ ! -s "$work/err" ]; then
		test-ok "$flag prints WASM runtime without loading the module"
	else
		test-fail "$flag: status=$status out=$(cat "$work/out") err=$(cat "$work/err")"
	fi
done
KAME_WASM="$work/missing.wasm" run --json --version
if [ "$status" = 0 ] && [ ! -s "$work/err" ] && node -e 'const i=JSON.parse(require("node:fs").readFileSync(process.argv[1],"utf8")); if(i.schema!==1||i.type!=="version"||i.runtime!=="WASM"||i.buildMode!=="wasm")process.exit(1)' "$work/out"; then
	test-ok "version JSON reports WASM runtime without loading the module"
else
	test-fail "version JSON: status=$status out=$(cat "$work/out") err=$(cat "$work/err")"
fi
test-step "JSON identity matches local provenance without loading even an invalid module"
node --input-type=module - "$CLI_ROOT/src/js/kame.js" "$work/identity" <<'JS'
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
const [source, root] = process.argv.slice(2);
const directory = join(root, 'wasm');
mkdirSync(directory, { recursive: true });
const wrapper = join(directory, 'kame.mjs');
const module = join(directory, 'kame.wasm');
const parent = join(root, 'PROVENANCE.json');
const local = join(directory, 'PROVENANCE.json');
copyFileSync(source, wrapper);
writeFileSync(module, 'not a WebAssembly module');
writeFileSync(join(root, 'Makefile.kmk'), 'default :\n\ttouch forbidden\n');
const metadata = {
  schema: 1, version: '0.3.0', sourceRevision: 'a'.repeat(40), created: '2026-10-17T00:00:00Z',
  subjects: [['kame.js', wrapper], ['kame.wasm', module]].map(([name, path]) => ({ name, sha256: createHash('sha256').update(readFileSync(path)).digest('hex') })),
};
const publish = (value, path = parent) => writeFileSync(path, JSON.stringify(value));
const query = (known, environment = {}) => {
  const result = spawnSync(process.execPath, [wrapper, '--json', '--version'], {
    cwd: root, env: { ...process.env, KAME_VERSION: 'label-only', KAME_WASM: '', ...environment }, encoding: 'utf8', timeout: 5000,
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, '');
  const value = JSON.parse(result.stdout);
  assert.deepEqual(value, { schema: 1, type: 'version', version: known ? metadata.version : 'label-only', buildID: known ? metadata.sourceRevision : 'unknown', buildTime: 'unknown', buildMode: 'wasm', runtime: 'WASM' });
};
query(false);
publish(metadata);
query(true); // Parent cache metadata, renamed wrapper, invalid WASM and misleading selector.
publish(metadata, local);
query(true);
writeFileSync(parent, 'invalid parent');
query(true); // Adjacent metadata takes priority.
publish(metadata);
for (const change of [
  value => { value.schema = 2; },
  value => { value.version = '../escape'; },
  value => { value.version = 'latest'; },
  value => { value.sourceRevision = 'unknown'; },
  value => { delete value.created; },
  value => { value.subjects.pop(); },
  value => { value.subjects.push(value.subjects[0]); },
  value => { value.subjects[0].sha256 = '0'.repeat(64); },
  value => { value.subjects[1].sha256 = '0'.repeat(64); },
  value => { value.subjects[0].sha256 = 'INVALID'; },
]) {
  const changed = structuredClone(metadata);
  change(changed);
  publish(changed, local);
  query(false); // An unusable first candidate must not fall back to valid parent metadata.
}
for (const invalid of ['{', 'null', '[]']) {
  writeFileSync(local, invalid);
  query(false);
}
rmSync(local);
symlinkSync(join(root, 'absent-metadata'), local);
query(false);
rmSync(local);
mkdirSync(local);
query(false);
rmSync(local, { recursive: true });
const override = join(root, 'override.wasm');
copyFileSync(module, override);
query(true, { KAME_WASM: override });
writeFileSync(override, 'different module');
query(false, { KAME_WASM: override });
query(false, { KAME_WASM: join(root, 'missing.wasm') });
writeFileSync(wrapper, readFileSync(wrapper, 'utf8') + '\n// changed wrapper\n');
query(false);
copyFileSync(source, wrapper);
rmSync(module);
query(false);
const human = spawnSync(process.execPath, [wrapper, '--version'], { encoding: 'utf8', env: { ...process.env, KAME_VERSION: 'label-only', KAME_WASM: '' }, timeout: 5000 });
assert.equal(human.status, 0, human.stderr);
assert.equal(human.stdout, 'kame label-only (runtime=WASM)\n');
assert.deepEqual(readdirSync(root).sort(), ['Makefile.kmk', 'PROVENANCE.json', 'override.wasm', 'wasm']);
console.log('matching, unavailable and mismatched provenance preserve fast, read-only version reporting');
JS
test-ok "local identity is distinct from version selectors and publisher authentication"

test-step "help prints usage"
run --help
if [ "$status" = 0 ] && [ -s "$work/out" ]; then
	test-ok "--help prints usage and exits 0"
else
	test-fail "--help: status=$status"
fi

test-step "CLI starts when invoked through an npm-style bin symlink"
ln -s "$CLI_ROOT/dist/kame.js" "$work/kame"
node "$work/kame" --version >"$work/out" 2>"$work/err"
if [ "$(cat "$work/out")" = "kame $(cat "$CLI_ROOT/VERSION") (runtime=WASM)" ] && [ ! -s "$work/err" ]; then
	test-ok "main-module detection follows the bin symlink"
else
	test-fail "bin symlink invocation: out=$(cat "$work/out") err=$(cat "$work/err")"
fi

test-step "do run --lang expr matches native output byte-for-byte"
for expression in '(count [1 2 3])' '(join ":" ["a" "b"])' '(uppercase "kame")' '(let [a 10] (out! "hello" a))' '(yield! "generated")'; do
	node "$CLI_ROOT/dist/kame.js" do run --lang expr -c "$expression" >"$work/wasm.out"
	"$CLI_BIN" do run --lang expr -c "$expression" >"$work/native.out"
	if cmp -s "$work/wasm.out" "$work/native.out"; then
		test-ok "parity: $expression"
	else
		test-fail "parity: $expression (wasm=$(cat "$work/wasm.out") native=$(cat "$work/native.out"))"
	fi
done
run do run --lang expr -c '(err! "notice")'
wasm_status=$status
cp "$work/out" "$work/wasm.err-effect.out"
cp "$work/err" "$work/wasm.err-effect.err"
"$CLI_BIN" do run --lang expr -c '(err! "notice")' >"$work/native.err-effect.out" 2>"$work/native.err-effect.err"
if [ "$wasm_status" = 0 ] && cmp -s "$work/wasm.err-effect.out" "$work/native.err-effect.out" && cmp -s "$work/wasm.err-effect.err" "$work/native.err-effect.err"; then
	test-ok "parity: err preserves stdout and stderr"
else
	test-fail "err parity: wasm-out=$(cat "$work/wasm.err-effect.out") wasm-err=$(cat "$work/wasm.err-effect.err") native-out=$(cat "$work/native.err-effect.out") native-err=$(cat "$work/native.err-effect.err")"
fi
printf '(join "-" ["wasm" "source"])\n' >"$work/expression.kmk"
node "$CLI_ROOT/dist/kame.js" do run --lang expr "$work/expression.kmk" >"$work/wasm.expr"
"$CLI_BIN" do run --lang expr "$work/expression.kmk" >"$work/native.expr"
if cmp -s "$work/wasm.expr" "$work/native.expr" && [ "$(cat "$work/wasm.expr")" = '"wasm-source"' ]; then
	test-ok "do run --lang expr FILE reads an expression file and matches native"
else
	test-fail "do run --lang expr FILE: wasm=$(cat "$work/wasm.expr") native=$(cat "$work/native.expr")"
fi

test-step "--shell selects the recipe shell on the wasm host"
cat >"$work/Shell.kmk" <<'EOF'
task shell-choice :
	echo should-not-run
EOF
run -C "$work" --shell "$work/missing-shell" --shell -c -f "$work/Shell.kmk" shell-choice
if [ "$status" = 1 ] && grep -q 'spawn .*missing-shell.*ENOENT' "$work/err"; then
	test-ok "recipe execution uses the selected shell"
else
	test-fail "selected shell ignored: status=$status out=$(cat "$work/out") err=$(cat "$work/err")"
fi

test-step "capability grants are denied by default"
run do run --lang expr -c '(read "VERSION")'
if [ "$status" = 1 ] && [ ! -s "$work/out" ] && grep -q 'CAP_DENIED' "$work/err"; then
	test-ok "read without --allow-read is denied"
else
	test-fail "read was not denied: status=$status out=$(cat "$work/out") err=$(cat "$work/err")"
fi
run do run --lang expr --allow-read -c '(read "VERSION")'
if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$(cat "$CLI_ROOT/VERSION")" ]; then
	test-ok "read with --allow-read succeeds"
else
	test-fail "read with grant failed: status=$status out=$(cat "$work/out")"
fi
run do run --lang expr --allow-run -c '(sh! "printf run-ok")'
if [ "$status" = 0 ] && [ "$(cat "$work/out")" = '[status: 0 stdout: run-ok stderr:  signal: 0 effectiveStatus: 0 effectiveSignal: 0]' ]; then
	test-ok "shell with --allow-run succeeds"
else
	test-fail "shell with grant failed: status=$status out=$(cat "$work/out")"
fi

test-step "diagnostic flags report the current stage"
run --wasm-abi-info
if [ "$status" = 0 ] && node -e 'const i=JSON.parse(require("node:fs").readFileSync(process.argv[1],"utf8")); if(i.schema!==1||i.stage!==2||!i.exports.includes("kame_wasm_step"))process.exit(1)' "$work/out"; then
	test-ok "--wasm-abi-info reports schema, stage, and exports"
else
	test-fail "--wasm-abi-info: $(cat "$work/out")"
fi
run --wasm-self-test
if [ "$status" = 0 ] && node -e 'const i=JSON.parse(require("node:fs").readFileSync(process.argv[1],"utf8")); if(!i.selfTest||i.selfTest.pure!=="\"a:b\"")process.exit(1)' "$work/out"; then
	test-ok "--wasm-self-test evaluates a pure expression"
else
	test-fail "--wasm-self-test: $(cat "$work/out")"
fi

test-step "unsupported work and usage errors use the right status"
run do nope
if [ "$status" = 2 ] && grep -q 'CMD_UNKNOWN' "$work/err"; then
	test-ok "unknown command exits 2"
else
	test-fail "unknown command: status=$status"
fi
run --nope
if [ "$status" = 2 ] && grep -q 'OPT_UNKNOWN' "$work/err"; then
	test-ok "unknown option exits 2"
else
	test-fail "unknown option: status=$status"
fi

test-end
