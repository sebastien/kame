#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — regular-file record iteration
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-08 Kash regular-file record loops"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make -C "$CLI_ROOT" dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
CLI_BIN="$CLI_BIN" WASM_RUNNER="$wasm_runner" RECORD_WORK="$TMPDIR" node <<'JS'
const { spawnSync } = require('node:child_process');
const { writeFileSync, mkdirSync } = require('node:fs');
const { join } = require('node:path');
const assert = require('node:assert/strict');
const work = process.env.RECORD_WORK;
mkdirSync(join(work, 'sub'));
const files = {
  lines: 'a\r\n\nb\rdata\nlast', nul: Buffer.from('a\0\0b\n\r\0last'), empty: '',
  lf: '\n', final: 'a\n', cr: 'a\r', embedded: Buffer.from('a\0b\n'),
  invalid: Buffer.from([0xff, 10]), tail: Buffer.from([97, 10, 0xff]),
  max: 'x'.repeat(65536), maxlf: 'x'.repeat(65535) + '\n',
  excess: 'x'.repeat(65537), excesslf: 'x'.repeat(65536) + '\n',
  many: 'a\n'.repeat(1000), 'sub/items': 'scoped\n',
  deadline: 'a\n'.repeat(1000000),
};
for (const [name, bytes] of Object.entries(files)) writeFileSync(join(work, name), bytes);
let checks = 0;
for (const runner of [[process.env.CLI_BIN], [process.execPath, process.env.WASM_RUNNER]]) {
  const run = (source, output, expected = 0, code, extra = []) => {
    const result = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-C', work, ...extra, '-l', 'kash', '-c', source], { input: 'borrowed', encoding: 'utf8', timeout: 12000, maxBuffer: 4 * 1024 * 1024 });
    assert.equal(result.error, undefined, source);
    assert.equal(result.status, expected, `${source}: ${result.stderr}`);
    assert.equal(result.stdout, output, source);
    if (code) assert.match(result.stderr, new RegExp(code), source);
    checks++;
  };
  run('for line from ./lines :lines\n\tprintf "[%s]" $line', '[a][][b\rdata][last]');
  run('for line from ./nul :nul\n\tprintf "[%s]" $line', '[a][][b\n\r][last]');
  run('for line from ./empty :lines\n\tprintf forbidden\nprintf X', 'X');
  run('for line from ./lf :lines\n\tprintf "[%s]" $line', '[]');
  run('for line from ./final :lines\n\tprintf "[%s]" $line', '[a]');
  run('for line from ./cr :lines\n\tprintf "[%s]" $line', '[a\r]');
  run('for line from ./lines :lines\n\tlet local = line\n\tprintf "[%s]" $local\n\tcontinue\n\tprintf forbidden', '[a][][b\rdata][last]');
  run('def input-file ()\n\tout! "S"\n\treturn ./final\nfor line from (input-file ()) :lines\n\tprintf %s $line', 'Sa');
  run('let source = "./final"\nfor line from source :lines\n\tprintf %s $line', 'a');
  run(`let source = (resource "file://${join(work, 'final')}")\nfor line from source :lines\n\tprintf %s $line`, 'a');
  run('for line from ./tail :lines\n\tprintf %s $line\n\tbreak\nprintf X', 'aX');
  run('for line from ./final :lines\n\tfor inner from ./lf :lines\n\t\tprintf %s%s $line $inner\n\t\tbreak\nprintf X', 'aX');
  run('for line from ./final :lines\n\twith\n\t\tdefer printf D\n\t\tprintf %s $line\n\t\tcontinue\nprintf X', 'aDX');
  run('def helper ()\n\tcat\n\treturn 0\nfor line from ./final :lines\n\t(helper ())\n\tprintf %s $line\ncat', 'aborrowed');
  run('def helper ()\n\tcat\n\treturn 0\ndef lazy = helper ()\nfor line from ./final :lines\n\tlet unused = lazy\n\tprintf %s $line\ncat', 'aborrowed');
  run('for line from ./final :lines\n\tcat < ./cr\n\tprintf %s $line\ncat', 'a\raborrowed');
  run('with cwd: ./sub\n\tfor line from ./sub/items :lines\n\t\ttest -f items\n\t\tprintf %s $line', 'scoped');
  run('for line from ./final :lines\n\twith\n\t\tdefer printf D\n\t\tsh -c "sleep 0.01; printf J" &\n\t\tbreak\nprintf X', 'JDX');
  run('def probe ()\n\tfor line from ./final :lines\n\t\twith\n\t\t\tdefer printf D\n\t\t\treturn 7\n\tprintf forbidden\nlet result = probe ()\nprintf %sX $result', 'D7X');
  run('for line from ./final :lines\n\twith\n\t\tdefer printf D\n\t\texit 23\nprintf forbidden', 'D', 23);
  run('def cleanup ()\n\tfor line from ./final :lines\n\t\tprintf %s $line\n\t\tbreak\ndefer (cleanup ())\nprintf X', 'Xa');
  run('for line from ./embedded :lines\n\tprintf %s $line', '', 1, 'EXPR_INVALID');
  run('for line from ./invalid :lines\n\tprintf forbidden', '', 1, 'KASH_FRAME_ENCODING');
  run('for line from ./max :lines\n\tprintf %s (count line)', '65536');
  run('for line from ./maxlf :lines\n\tprintf %s (count line)', '65535');
  run('for line from ./excess :lines\n\tprintf forbidden', '', 1, 'KASH_RECORD_LIMIT');
  run('for line from ./excesslf :lines\n\tprintf forbidden', '', 1, 'KASH_RECORD_LIMIT');
  run('for line from [] :lines\n\tprintf forbidden', '', 1, 'EXPR_INVALID');
  run('for line from "" :lines\n\tprintf forbidden', '', 1, 'EXPR_INVALID');
  run('for line from ./absent :lines\n\tprintf forbidden', '', 1, 'FS_ERR');
  run('for line from ./sub :lines\n\tprintf forbidden', '', 1, 'FS_ERR');
  run('for line from ./final :lines\n\tprintf forbidden', '', 1, 'CAP_DENIED', ['--allow-run']);
  run('for line from ./absent :lines\n\tprintf forbidden\n\tdefer printf forbidden', '', 0, undefined, ['-n']);
  run('for line from ./many :lines\n\twith\n\t\tdefer (nop :nil)\n\t\tcontinue\nprintf X', 'X');
  run('defer printf D\nfor line from ./deadline :lines\n\tcontinue', 'D', 1, 'RECIPE_TIMEOUT', ['--timeout', '500']);
  run('def probe ()\n\tfor line from ./invalid :lines\n\t\tprintf forbidden\nlet result = $?(probe)\nif result.state != :failed\n\t(fail! "expected a failed record reader")\nfor line from ./final :lines\n\tprintf %s $line', 'a');
  writeFileSync(join(work, 'replace'), 'old1\nold2\n'); writeFileSync(join(work, 'replacement'), 'new\n');
  run('for line from ./replace :lines\n\tif line == "old1"\n\t\tmv ./replacement ./replace\n\tprintf "%s;" $line', 'old1;old2;');
  const formatted = spawnSync(runner[0], [...runner.slice(1), 'do', 'fmt', '-l', 'kash', '-c', 'for line from ./final :lines\n\tprintf %s $line'], { encoding: 'utf8', timeout: 6000 });
  assert.equal(formatted.status, 0, formatted.stderr); run(formatted.stdout, 'a');
  const ast = spawnSync(runner[0], [...runner.slice(1), 'do', 'parse', '--json', '-l', 'kash', '-c', formatted.stdout], { encoding: 'utf8', timeout: 6000 });
  assert.equal(ast.status, 0, ast.stderr); assert.match(ast.stdout, /"kind":"kash-records"/); assert.match(ast.stdout, /"frame":":lines"/); checks++;
}
console.log(`${checks} native/Node regular-file record checks passed`);
JS
test-ok "native/Node framing, limits, lexical scope, source identity and lifecycle"
test-end
