#!/usr/bin/env bash
# Spec: docs/spec/018-source-style.md — canonical expression layouts
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T018-01 canonical source style"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}

test-step "goldens, width boundaries, AST stability and host parity"
python3 - "$CLI_BIN" "$wasm_wrapper" "$TMPDIR" <<'PY'
import json
import pathlib
import subprocess
import sys

native, wasm, work = sys.argv[1:]
work = pathlib.Path(work)
long = 'A' * 70
huge = 'B' * 90
cases = [
    ('expr', '(x=>\n f x)', '(x => f x)'),
    ('expr', f'(f {long} x y z w)', f'(\n\tf\n\t\t{long}\n\t\tx\n\t\ty\n\t\tz\n\t\tw\n)'),
    ('expr', f'f {long} x y z w', f'f {long} x y z w'),
    ('expr', f'f x [first, {long},last,]', f'f x [\n\tfirst\n\t{long}\n\tlast\n]'),
    ('expr', f'(if (ready? {long}) then yes elif other then nil else no)',
     f'(if (\n\tready?\n\t\t{long}\n) then yes\n\telif other then nil\n\telse no\n)'),
    ('expr', f'if condition then {long} else no', f'if condition then {long} else no'),
    ('expr', f'(x rest...=>f {long} x)', f'(x rest... =>\n\tf\n\t\t{long}\n\t\tx\n)'),
    ('expr', f'[first,{long},last]', f'[\n\tfirst\n\t{long}\n\tlast\n]'),
    ('expr', f'{{name:x,data:{long}}}', f'{{\n\tname: x,\n\tdata:\n\t\t{long},\n}}'),
    ('expr', f'{{long-key:(f {long} x),other:y}}',
     f'{{\n\tlong-key:\n\t\t(\n\t\t\tf\n\t\t\t\t{long}\n\t\t\t\tx\n\t\t),\n\tother: y,\n}}'),
    ('expr', f'match x\n  "p" => (f {long} x)\n  "some" => 42\n  else => nil',
     f'match x\n\t"p" =>\n\t\t(\n\t\t\tf\n\t\t\t\t{long}\n\t\t\t\tx\n\t\t)\n\t"some" => 42\n\telse   => nil'),
    ('expr', f'((choose {long} x) y)',
     f'(\n\t(\n\t\tchoose\n\t\t\t{long}\n\t\t\tx\n\t)\n\t\ty\n)'),
    ('expr', '(f """first\n  literal\t\nlast""" x)',
     '(\n\tf\n\t\t"""first\n  literal\t\nlast"""\n\t\tx\n)'),
    ('km', f'value = (f {long} x)\n', f'value =\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\tx\n\t)\n'),
    ('km', f'value ?= (f {long} x)\n', f'value ?=\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\tx\n\t)\n'),
    ('km', f'def helper first {long} rest... = f first\n',
     f'def helper first {long} rest... =\n\tf first\n'),
    ('km', f'value = {huge}\n', f'value =\n\t{huge}\n'),
    ('km', f'\n# Group\n\nvalue = (f {long} x)\n\n\nnext = 1\n\n',
     f'\n# Group\n\nvalue =\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\tx\n\t)\n\n\nnext = 1\n\n'),
    ('km', 'value = f x \\\n y   \nnext = 1\n', 'value = f x y\nnext = 1\n'),
    ('expr', f'(f {huge})', f'(\n\tf\n\t\t{huge}\n)'),
    ('expr', '[]', '[]'),
    ('expr', 'HELP |>  map std-help-line |>  join "\\n"',
     'HELP |> map std-help-line |> join "\\n"'),
    ('expr', 'value |> pair _ _ |> transform a _ b',
     'value |> pair _ _ |> transform a _ b'),
    ('expr', '(join " " (map transform value))',
     '(join " " (map transform value))'),
    ('expr', f'{long} |> map navigation-item |> filter (item => item.visible) |> join "\\n"',
     f'{long}\n\t|> map navigation-item\n\t|> filter (item => item.visible)\n\t|> join "\\n"'),
    ('expr', f'value |> transform (nested {long} data) _ |> finish',
     f'value\n\t|> transform (\n\t\tnested\n\t\t\t{long}\n\t\t\tdata\n\t) _\n\t|> finish'),
    ('expr', f'"${{{long} |> transform _ b}}"', f'"${{{long} |> transform _ b}}"'),
    ('kash', f'printf "%s" prefix-({long} |> transform _ b).txt\n',
     f'printf "%s" prefix-({long} |> transform _ b).txt\n'),
    ('expr', f'"${{f {long} x}}"', f'"${{f {long} x}}"'),
    ('template', f'text @{{f {long} x}} end', f'text @{{f {long} x}} end'),
    ('template', '  before @{match "value"\n    "value" => "first"\n  } after\n',
     '  before @{\nmatch "value"\n\t"value" => "first"} after\n'),
    ('kash', f'def value = (f {long} x)\necho $value\n',
     f'def value =\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\tx\n\t)\necho $value\n'),
    ('kash', f'let value = (f {long} x)\nset value = (f {long} y)\n',
     f'let value =\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\tx\n\t)\nset value =\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\ty\n\t)\n'),
    ('kash', f'def f ()\n\tlet value = "{long}"\n\tset value = "{huge}"\n\tprintf %s $value\n',
     f'def f ()\n\tlet value =\n\t\t"{long}"\n\tset value =\n\t\t"{huge}"\n\tprintf %s $value\n'),
    ('expr', '$(echo "$name"/suffix)', '$(echo "$name"/suffix)'),
    ('expr', '$(echo "$name"/)', '$(echo "$name"/)'),
    ('kash', 'echo "$build/changed"/suffix\n', 'echo "$build/changed"/suffix\n'),
    ('kash', 'echo "$name""/suffix file"\n', 'echo "$name""/suffix file"\n'),
    ('expr', f'{long} ?? other ?? final', f'{long}\n\t?? other\n\t?? final'),
    ('expr', '(join   __  _)', '(join __ _)'),
    ('km', 'rule ./out : ./in , ./generated  |   ./dir\n\tcat $...inputs > $outputs.0\n',
     'rule ./out : ./in, ./generated | ./dir\n\tcat $...inputs > $outputs.0\n'),
    ('km', 'task clean :\n', 'task clean\n'),
    ('km', f'task default ; {{shell:"sh", env:{{VALUE:"{long}"}}}}\n\techo yes\n',
     f'task default ; {{\n\tshell: "sh",\n\tenv:\n\t\t{{\n\t\t\tVALUE:\n\t\t\t\t"{long}",\n\t\t}},\n}}\n\techo yes\n'),
    ('km', f'rule ./out = (f {long} x)\n',
     f'rule ./out =\n\t(\n\t\tf\n\t\t\t{long}\n\t\t\tx\n\t)\n'),
    ('km', f'# keep block\nrule ./out ; {{label:{long} |> map transform |> join " "}} = "body" # keep header\n',
     f'# keep block\nrule ./out ; {{\n\tlabel:\n\t\t{long}\n\t\t\t|> map transform\n\t\t\t|> join " ",\n}} = "body" # keep header\n'),
    ('kash', 'printf a |(each) | cat\n', 'printf a |(each) cat\n'),
    ('kash', 'printf a |(broadcast) (cat > ./l, cat > ./r)\n',
     'printf a |(broadcast) (\n\tcat > ./l,\n\tcat > ./r\n)\n'),
    ('kash', 'printf a |(frame :lines) |(filter true | cat) | cat\n',
     'printf a |(frame :lines) |(filter true |\n\tcat\n) | cat\n'),
    ('km', 'if (out! "formatter-forbidden")\n  include ./formatter-forbidden\n',
     'if (out! "formatter-forbidden")\n\tinclude ./formatter-forbidden\n'),
    ('km', 'from ./lib/c import compile,object-path   as obj\n',
     'from ./lib/c import compile, object-path as obj\n'),
    ('km', 'MiXeD_Name  =  def-name Another_Name\n',
     'MiXeD_Name = def-name Another_Name\n'),
]

# Exact 80/81 columns, Unicode, definition prefixes and tab-indented statements.
for char in ('a', 'é'):
    for size in (74, 75):
        argument = f'"{char * size}"'
        text = f'(f {argument})'
        assert len(text) == size + 6
        expected = text if size == 74 else f'(\n\tf\n\t\t{argument}\n)'
        cases.append(('expr', text, expected))
for size in (70, 71):
    argument = f'"{"a" * size}"'
    text = f'V = (f {argument})\n'
    expected = text if size == 70 else f'V =\n\t(\n\t\tf\n\t\t\t{argument}\n\t)\n'
    cases.append(('km', text, expected))
for size in (63, 64):
    argument = f'"{"a" * size}"'
    text = f'rule ./x = (f {argument})\n'
    assert len(text.rstrip('\n')) == size + 17
    expected = text if size == 63 else f'rule ./x =\n\t(f {argument})\n'
    cases.append(('km', text, expected))
for prefix in ('$(', '$?(', '$&('):
    command = f'printf {huge}'
    cases.append(('expr', prefix + command + ')', f'{prefix}\n\t{command}\n)'))
cases.append(('km', 'V = """line\nraw\t\nend"""\n',
              'V =\n\t"""line\nraw\t\nend"""\n'))
cases.append(('km', 'task x ; {shell:"sh"}\n\techo base\n\t  echo literal\n',
              'task x ; {shell: "sh"}\n\techo base\n\t  echo literal\n'))

def run(runner, command, lang, text):
    file = work / 'style.km'
    file.write_text(text)
    options = ['--json'] if command == 'parse' else []
    result = subprocess.run(runner + ['do', command, *options, '--lang', lang, str(file)],
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
        want = expected + "\n" if lang == "expr" else expected
        assert output == want, (index, runner, repr(output), repr(want))
        assert run(runner, 'fmt', lang, output) == output, (index, 'idempotence')
        before = ast(json.loads(run(runner, 'parse', lang, text)))
        after = ast(json.loads(run(runner, 'parse', lang, output)))
        assert before == after, (index, 'AST changed', before, after)

# The execution reader must also accept formatter-created multiline headers/RHS.
for runner in ([native], ['node', wasm]):
    text = f'def helper input {long} rest... = if true then input else nil\nanswer = helper 42 0\n'
    output = run(runner, 'fmt', 'km', text)
    file = work / 'execute.km'
    file.write_text(output)
    result = subprocess.run(runner + ['do', 'run', str(file), 'answer'], capture_output=True, text=True)
    assert result.returncode == 0 and result.stdout == '42', (runner, result)
    body = f'def emit value = str "prefix:" value\nlet value = "{long}"\nset value = emit "{huge}"\nprintf %s $value\n'
    output = run(runner, 'fmt', 'kash', body)
    file = work / 'execute.kash'
    file.write_text(output)
    result = subprocess.run(runner + ['do', 'run', '--allow-run', str(file)], capture_output=True, text=True)
    assert result.returncode == 0 and result.stdout == 'prefix:' + huge, (runner, result)
assert not (work / 'formatter-forbidden').exists()
assert not (pathlib.Path.cwd() / 'formatter-forbidden').exists()
print(f'{len(cases)} goldens passed on native and WASM')
PY
test-ok "source-style goldens are stable and identical on both hosts"
test-end
