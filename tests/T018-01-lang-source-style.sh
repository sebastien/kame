#!/usr/bin/env bash
# Spec: docs/spec/018-source-style.md — canonical expression layouts
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T018-01 canonical source style"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

test-step "goldens, width boundaries, AST stability and host parity"
python3 - "$CLI_BIN" "$CLI_ROOT/dist/kame.js" "$TMPDIR" <<'PY'
import json
import pathlib
import subprocess
import sys

native, wasm, work = sys.argv[1:]
work = pathlib.Path(work)
long = 'A' * 70
huge = 'B' * 90
cases = [
    ('expr', '(let\n [x 1]\n (f x))', '(let [x 1] (f x))'),
    ('expr', f'(f {long} x y z w)', f'(f\n  {long}\n  x\n  y\n  z\n  w)'),
    ('expr', f'(let [x {long} y 2222] (f x y))',
     f'(let\n  [x {long}\n   y 2222]\n  (f x y))'),
    ('expr', f'(let [x (f {long} y) z 2] x)',
     f'(let\n  [x\n     (f\n       {long}\n       y)\n   z 2]\n  x)'),
    ('expr', f'(if (ready? {long}) yes no)',
     f'(if\n  (ready?\n    {long})\n    yes\n  no)'),
    ('expr', f'(if condition {long})', f'(if\n  condition\n    {long})'),
    ('expr', f'([x rest...] (f {long} x))',
     f'([x rest...]\n  (f {long} x))'),
    ('expr', f'[first {long} last]', f'[\n  first\n  {long}\n  last]'),
    ('expr', f'[name: x data: {long}]', f'[\n  name: x\n  data: {long}]'),
    ('expr', f'[long-key: (f {long} x) other: y]',
     f'[\n  long-key:\n    (f {long} x)\n  other: y]'),
    ('expr', f'(match x ["p" (f {long} x)] [:else :nil])',
     f'(match\n  x\n  ["p"\n    (f\n      {long}\n      x)]\n  [:else :nil])'),
    ('expr', f'((choose {long} x) y)',
     f'((choose\n  {long}\n  x)\n  y)'),
    ('expr', '(f """first\n  literal\t\nlast""" x)',
     '(f\n  """first\n  literal\t\nlast"""\n  x)'),
    ('script', f'value = (f {long} x)\n', f'value =\n  (f {long} x)\n'),
    ('script', f'value ?= (f {long} x)\n', f'value ?=\n  (f {long} x)\n'),
    ('script', f'(helper first {long} rest...) = (f first)\n',
     f'(helper\n  first\n  {long}\n  rest...) =\n  (f first)\n'),
    ('script', f'value = {huge}\n', f'value =\n  {huge}\n'),
    ('script', f'\n# Group\n\nvalue = (f {long} x)\n\n\n(other x)\n\n',
     f'\n# Group\n\nvalue =\n  (f {long} x)\n\n\n(other x)\n\n'),
    ('script', '(f\n x\n y)   \nnext = 1\n', '(f x y)\nnext = 1\n'),
    ('expr', f'(f {huge})', f'(f\n  {huge})'),
    ('expr', '[]', '[]'),
    ('expr', f'"@(f {long} x)"', f'"@(f {long} x)"'),
    ('template', f'text @(f {long} x) end', f'text @((f {long} x)) end'),
    ('kash', f'value = (f {long} x)\necho @(value)\n',
     f'value = (f {long} x)\n"echo" @(value)\n'),
]
# Exact 80/81 columns, Unicode, definition prefixes and nested closing delimiters.
for char in ('a', 'é'):
    for size in (74, 75):
        text = f'(f "{char * size}")'
        assert len(text) == size + 6
        want = text if size == 74 else f'(f\n  "{char * size}")'
        cases.append(('expr', text, want))
for size in (70, 71):
    text = f'V = (f "{"a" * size}")\n'
    want = text if size == 70 else f'V =\n  (f "{"a" * size}")\n'
    cases.append(('script', text, want))
cases.append(('expr', '(f (g "' + 'a' * 73 + '") x)',
              '(f\n  (g\n    "' + 'a' * 73 + '")\n  x)'))
cases.append(('expr', '(f """' + '\t' * 10 + 'x""")',
              '(f\n  """' + '\t' * 10 + 'x""")'))
cases.append(('expr', '([' + 'p ' * 40 + 'rest...] x)',
              '([\n' + '  p\n' * 40 + '  rest...]\n  x)'))
cases.append(('expr', f'(f (g "{"a" * 71}"))',
              f'(f\n  (g "{"a" * 71}"))'))
cases.append(('script', f'V = "{"a" * 75}"\n',
              f'V =\n  "{"a" * 75}"\n'))
cases.append(('script', f'V = """line\nraw\t\nend"""\n',
              f'V =\n  """line\nraw\t\nend"""\n'))
cases.append(('script', f'(f {long} x)\nnext = 1\n',
              f'(f {long} x)\nnext = 1\n'))
cases.append(('script', f'value = (f {long} x)\n./out : ./in\n\techo base\n\t  echo literal\n',
              f'value =\n  (f {long} x)\n./out : ./in\n\techo base\n\t  echo literal\n'))

# Declaration predicates include their five-column prefix in layout decisions.
for size in (69, 70):
    predicate = f'(f "{"a" * size}")'
    text = f'when {predicate}\nV = 1\nend\n'
    want = text if size == 69 else f'when (f\n       "{"a" * size}")\nV = 1\nend\n'
    cases.append(('script', text, want))
# Formatting preserves effectful syntax without invoking it.
cases.append(('script', 'when (shell "touch formatter-forbidden")\nV = 1\nend\n',
              'when (shell "touch formatter-forbidden")\nV = 1\nend\n'))

def run(runner, command, lang, text):
    file = work / 'style.km'
    file.write_text(text)
    result = subprocess.run(runner + ['do', command, '--lang', lang, str(file)],
                            capture_output=True, text=True)
    assert result.returncode == 0, (command, lang, text, result.stderr)
    return result.stdout

def ast(value):
    if isinstance(value, dict):
        return {k: ast(v) for k, v in value.items()
                if k not in ('span', 'start', 'end', 'source')}
    if isinstance(value, list):
        return [ast(v) for v in value]
    return value

for index, (lang, text, expected) in enumerate(cases):
    for runner in ([native], ['node', wasm]):
        output = run(runner, 'fmt', lang, text)
        want = expected
        assert output == want, (index, runner, repr(output), repr(want))
        assert run(runner, 'fmt', lang, output) == output, (index, 'idempotence')
        before = ast(json.loads(run(runner, 'parse', lang, text)))
        after = ast(json.loads(run(runner, 'parse', lang, output)))
        assert before == after, (index, 'AST changed', before, after)

# The execution reader must also accept formatter-created multiline headers/RHS.
for runner in ([native], ['node', wasm]):
    text = f'(helper input {long} rest...) = (if :true input)\n(helper 42 0)\n'
    output = run(runner, 'fmt', 'script', text)
    file = work / 'execute.km'
    file.write_text(output)
    result = subprocess.run(runner + ['do', 'run', str(file)], capture_output=True, text=True)
    assert result.returncode == 0 and result.stdout == '42', (runner, result)
assert not (work / 'formatter-forbidden').exists()
assert not (pathlib.Path.cwd() / 'formatter-forbidden').exists()
print(f'{len(cases)} goldens passed on native and WASM')
PY
test-ok "source-style goldens are stable and identical on both hosts"
test-end
