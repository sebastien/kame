#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — selected ordinary exits
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-10 Kash selected ordinary exits"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
CLI_BIN="$CLI_BIN" CLI_ROOT="$CLI_ROOT" SELECTED_WORK="$TMPDIR" node <<'JS'
const {spawnSync} = require('node:child_process');
const {writeFileSync, readFileSync, existsSync} = require('node:fs');
const {join} = require('node:path');
const assert = require('node:assert/strict');
let checks = 0;
for (const runner of [[process.env.CLI_BIN], [process.execPath, join(process.env.CLI_ROOT, 'dist/kame.js')]]) {
  const run = (source, out = '', status = 0, code, extra = []) => {
    const r = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-C', process.env.SELECTED_WORK, '--allow-run', '--allow-read', '--allow-write', ...extra, '-l', 'kash', '-c', source], {encoding:'utf8', timeout:12000});
    assert.equal(r.error, undefined, source); assert.equal(r.status, status, `${source}\n${r.stderr}`); if (out !== null) assert.equal(r.stdout, out, source);
    if (code) assert.match(r.stderr, new RegExp(code)); checks++; return r;
  };
  run(':accept-exit [1] sh -c "exit 1"\nprintf OK', 'OK');
  run(':accept-exit [1] sh -c "exit 1"');
  run(':accept-exit [1 7 255] sh -c "exit 255"');
  run(':accept-exit [] true');
  run(':accept-exit [] sh -c "exit 7"', '', 7, 'RECIPE_FAIL');
  run(':accept-exit [1] sh -c "exit 7"', '', 7, 'RECIPE_FAIL');
  run('policy = [1]\n:accept-exit $policy sh -c "exit 1" && printf Y', 'Y');
  run(':accept-exit [1] sh -c "exit 1" || printf forbidden');
  run('if :accept-exit [1] sh -c "exit 1"\n\tprintf Y\nelse\n\tprintf forbidden', 'Y');
  run('printf %s $(:accept-exit [1] sh -c "printf exact; exit 1")', 'exact');
  run(':accept-exit [1] sh -c "exit 1" | cat');
  run('(sh -c "exit 7", :accept-exit [1] sh -c "exit 1") |(merge :failure collect) cat', '', 7, 'RECIPE_FAIL');
  run('(:accept-exit [7] sh -c "exit 7", :accept-exit [1] sh -c "exit 1") |(merge) cat');
  run('(:accept-exit [7] sh -c "exit 7", :accept-exit [1] sh -c "exit 1") |(merge :failure collect) cat &');
  run(':accept-exit [143] sh -c "kill -TERM \\$\\$"', '', 143, 'RECIPE_FAIL');
  run(':accept-exit [127] missing-selected-exit-tool', '', 1, 'TOOL_MISSING');
  run(':accept-exit [1] :timeout 0.03 sh -c "sleep 3; exit 1"', '', 1, 'RECIPE_TIMEOUT');
  run('printf %s $(:accept-exit [1] sh -c "printf excessive; exit 1")', '', 1, 'CAPTURE_LIMIT', ['--capture-limit', '2']);
  for (const policy of ['[0]', '[256]', '[-1]', '[1 1]', '[1.0]', '["1"]', ':nil', '"1"']) {
    const dest = join(process.env.SELECTED_WORK, 'selected-output'), marker = join(process.env.SELECTED_WORK, 'selected-marker');
    writeFileSync(dest, 'preserve');
    run(`:accept-exit ${policy} sh -c "touch ${marker}" > ${dest}`, '', 1, 'EXPR_INVALID');
    assert.equal(readFileSync(dest, 'utf8'), 'preserve'); assert.equal(existsSync(marker), false);
  }
  run(':accept-exit (read! ./missing-policy) true', '', 0, undefined, ['-n']);
  run('if (eq? 1 2)\n\t:accept-exit (read! ./missing-policy) true\nprintf Y', 'Y');
  const r = run('let r = (run! :accept-exit [1] "sh" "-c" "exit 1")\nprintf %s (json r)', null);
  // Inspect separately: the Result keeps raw status, while joins use effective status.
  const value = JSON.parse(r.stdout); assert.equal(value.status, 1); assert.equal(value.effectiveStatus, 0); assert.equal(value.stages[0].status, 1);
  const worker = 'sh -c "read code; exit \\$code"';
  run(`printf "1\\n1\\n" |(frame :lines) |(each) :accept-exit [1] ${worker}`);
  run(`(printf "7\\n1\\n" |(frame :lines) |(each) :accept-exit [1] ${worker}, printf "") |(merge :failure collect) cat`, '', 7, 'RECIPE_FAIL');
  run(`(printf "1\\n7\\n" |(frame :lines) |(each) :accept-exit [1] ${worker}, printf "") |(merge :failure collect) cat`, '', 7, 'RECIPE_FAIL');
  const formatted = spawnSync(runner[0], [...runner.slice(1), 'do', 'fmt', '-l', 'kash', '-'], {input: ':accept-exit [1 7] true\n', encoding:'utf8'});
  assert.equal(formatted.status, 0, formatted.stderr); assert.match(formatted.stdout, /:accept-exit \[1 7\]/); checks++;
}
console.log(`${checks} native/Node selected-exit checks passed`);
JS
test-ok "selected ordinary exits preserve raw results and effective failure provenance"
test-end
