"""Native/WASM JSON operation parity and generated-read regression."""
import subprocess
import tempfile
import json
from pathlib import Path

root = Path(__file__).resolve().parent.parent
native = root / 'build/kame.debug'
wasm = root / 'dist/kame.js'
expressions = [
    '(parse-json " {\\\"name\\\":\\\"Texto\\\",\\\"items\\\":[true,false,null,-42,1.5]} \\n")',
    '(parse-json "\\\"\\\\ud83d\\\\ude80\\\"")',
    '(let [page (parse-json "{\\\"title\\\":\\\"News\\\"}")] page.title)',
    '(parse-json "01")', '(parse-json "[1,]")', '(parse-json "[bad]")', '(parse-json 42)',
]
for expression in expressions:
    first = subprocess.run([str(native), 'do', 'expr', '-c', expression], capture_output=True)
    second = subprocess.run(['node', str(wasm), 'do', 'expr', '-c', expression], capture_output=True)
    assert first.returncode == second.returncode, (expression, first.stderr, second.stderr)
    assert first.stdout == second.stdout, (expression, first.stdout, second.stdout)
    if first.returncode:
        code = 'EXPR_INVALID' if expression.endswith('42)') else 'JSON_INVALID'
        assert code.encode() in first.stderr and code.encode() in second.stderr, (expression, first.stderr, second.stderr)
    else:
        assert first.stdout, expression

with tempfile.TemporaryDirectory(prefix='kame-json-') as directory:
    project = Path(directory)
    (project / 'Makefile.kmk').write_text('''default : ./page.html
./data.json :
	@(yield "{\\\"title\\\":\\\"Generated\\\"}")
./page.html :
	@(yield (let [data (parse-json (text (read ./data.json)))] data.title))
''')
    result = subprocess.run([str(native)], cwd=project, capture_output=True)
    assert result.returncode == 0, result.stderr
    assert (project / 'page.html').read_text() == 'Generated'
    # Absolute requests must select relative template producers too.
    (project / 'Makefile.kmk').write_text('''default : ./page.html
./{name:*}.json :
	@(yield "{\\\"title\\\":\\\"Template\\\"}")
./page.html :
	@(yield (let [data (parse-json (text (read ./other.json)))] data.title))
''')
    result = subprocess.run([str(native)], cwd=project, capture_output=True)
    assert result.returncode == 0, result.stderr
    assert (project / 'page.html').read_text() == 'Template'
result = subprocess.run(['node', str(wasm), '--watch'], capture_output=True)
assert result.returncode != 0 and b'--watch' in result.stderr and b'native' in result.stderr
print('JSON native/WASM parity and generated-read checks passed.')
