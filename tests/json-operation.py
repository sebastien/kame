"""Native/WASM JSON operation parity and generated-read regression."""
import os
import subprocess
import tempfile
import json
from pathlib import Path

root = Path(__file__).resolve().parent.parent
native = Path(os.environ.get('CLI_BIN', root / 'build/kame.debug'))
wasm = Path(os.environ.get('KAME_TEST_WASM_WRAPPER', root / 'dist/kame.js'))
expressions = [
    '(parse-json " {\\\"name\\\":\\\"Texto\\\",\\\"items\\\":[true,false,null,-42,1.5]} \\n")',
    '(parse-json "\\\"\\\\ud83d\\\\ude80\\\"")',
    '(parse-json "{\\\"title\\\":\\\"News\\\"}").title',
    '(parse-json "01")', '(parse-json "[1,]")', '(parse-json "[bad]")', '(parse-json 42)',
]
for expression in expressions:
    first = subprocess.run([str(native), 'do', 'run', '--lang', 'expr', '-c', expression], capture_output=True, timeout=30)
    second = subprocess.run(['node', str(wasm), 'do', 'run', '--lang', 'expr', '-c', expression], capture_output=True, timeout=30)
    assert first.returncode == second.returncode, (expression, first.stderr, second.stderr)
    assert first.stdout == second.stdout, (expression, first.stdout, second.stdout)
    if first.returncode:
        code = 'EXPR_INVALID' if expression.endswith('42)') else 'JSON_INVALID'
        assert code.encode() in first.stderr and code.encode() in second.stderr, (expression, first.stderr, second.stderr)
    else:
        assert first.stdout, expression

with tempfile.TemporaryDirectory(prefix='kame-json-') as directory:
    for backend, command in [('native', [str(native)]), ('wasm', ['node', str(wasm)])]:
        project = Path(directory) / backend
        project.mkdir()
        (project / 'Makefile.km').write_text('''task default : ./page.html
rule ./data.json = "{\\\"title\\\":\\\"Generated\\\"}"
rule ./page.html = (parse-json (text (read ./data.json))).title
''')
        result = subprocess.run(command, cwd=project, capture_output=True, timeout=30)
        assert result.returncode == 0, (backend, result.stderr)
        assert (project / 'page.html').read_text() == 'Generated', backend
        # Absolute requests must select relative template producers too.
        (project / 'Makefile.km').write_text('''task default : ./page.html
rule ./{name:*}.json = "{\\\"title\\\":\\\"Template\\\"}"
rule ./page.html = (parse-json (text (read ./other.json))).title
''')
        result = subprocess.run(command, cwd=project, capture_output=True, timeout=30)
        assert result.returncode == 0, (backend, result.stderr)
        assert (project / 'page.html').read_text() == 'Template', backend
# Watch lifecycle conformance lives in the dedicated WASM watch tests; watch is
# supported and intentionally does not terminate as a one-shot JSON probe.
print('JSON native/WASM parity and generated-read checks passed.')
