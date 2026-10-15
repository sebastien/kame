# Embedding Kame

Use embedding when a host application needs values, builds, or watch events
without parsing human CLI output. A **client** owns reusable **programs** compiled
from source; programs evaluate expressions or build named targets. A **watch**
keeps a build session alive until explicitly closed.

JavaScript runs the freestanding WASM engine with Node host services. Python is
an asynchronous subprocess client for the CLI, not a Python WASM binding.
Compilation validates source; it does not build targets. Definitions are lazy.

## JavaScript: source checkout

Requires Node 18+. From the repository root, run `make dist-wasm` if the artifacts
are absent. Save this as `example.mjs` in that root, then run `node example.mjs`:

```js
import { Kame } from './dist/kame.js';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const cwd = await mkdtemp(join(tmpdir(), 'kame-example-'));
const kame = await Kame.create({ wasmPath: './dist/kame.wasm' });
try {
  const values = await kame.compile('answer = 42\n');
  console.log(await values.evaluate('(count [1 2 3])')); // 3
  console.log((await values.build('answer')).results[0].value); // 42
  const program = await kame.compile('./hello.txt :\n\t@(yield! "hello")\n',
    { name: 'example.kmk' });
  await program.build('./hello.txt', {
    cwd,
    grants: [
      { capability: 'read', names: [cwd] },
      { capability: 'write', names: [cwd] },
    ],
  });
  console.log(await readFile(join(cwd, 'hello.txt'), 'utf8')); // hello
} finally {
  await kame.dispose(); // also disposes both programs
  await rm(cwd, { recursive: true, force: true });
}
```

Importing does not dispatch the CLI. `src/js/kame.js` also works with an explicit
`wasmPath` or `KAME_WASM`; otherwise it needs adjacent `kame.wasm`. No bare-package embedding export is configured.
`compile(source, { name, language })` defaults to `input.km` and `script` parsing;
the filename alone does not select a parser. `kame.parse(language, source)` returns
an AST document, `kame.format(language, source)` formatted text, and `program.ast`
a copied AST document. `format` also accepts `indentStyle` and `indentWidth`.
Execution options belong to each `evaluate`, `build`, or `watch` call, not `create`.

## Python: source checkout

Requires Python 3.11+ and a CLI executable (`make dist` supplies `dist/kame`).
Run from the repository root with `PYTHONPATH=src/py python3 example.py`.
`python3 -m pip install -e .` installs `kame-embed` (import `kame`), not the CLI.

```python
import asyncio
import tempfile
from pathlib import Path
from kame import Kame

async def main():
    executable = str(Path('dist/kame').resolve())
    with tempfile.TemporaryDirectory() as cwd:
        async with Kame(executable, cwd=cwd) as kame:
            print(await kame.evaluate('(count [1 2 3])'))  # 3
            values = await kame.compile('answer = 42\n')
            print((await values.build('answer'))['results'][0]['value']['data'])  # 42
            program = await kame.compile(
                './hello.txt :\n\t@(yield! "hello")\n', name='example.kmk')
            await program.build('./hello.txt', grants={'read': ['.'], 'write': ['.']})
            print(Path(cwd, 'hello.txt').read_text())  # hello

asyncio.run(main())
```

`Kame()` otherwise uses executable `kame` on PATH. Constructor `cwd`, `env`, and
`grants` apply to its requests; a call's `grants` replaces the constructor mapping.
`compile` infers `km`, `kmk`, or `kash` from the name (default `embedded.km`), or
accepts explicit `language`. Program expression evaluation requires `km`.
Each operation launches a CLI process; compiled sources live in temporary files.

## Results and authority

- Pure evaluation returns text, not a native JS/Python value: `42`, `:true`,
  `[value: 1]`, or a quoted Kame string. Byte results can be raw UTF-8 text.
  Python returns captured stdout with trailing newlines removed, so `out!` output
  is mixed with the displayed result; stderr is not part of that value.
- Both builds return `{ results, events }` in target/event order; `targets` accepts one string or multiple names.
  For `answer = 42`, JS has `results: [{ target: 'answer', kind: 1, value: '42' }]`;
  Python has `results: [{'target': 'answer', 'value': {'kind': 'int', 'data': 42}}]`.
  Python uses `None` when no matching value event exists. Events are JSON records.
- JS grants are arrays, e.g. `[{ capability: 'read', names: ['./data'] }]`.
  Capabilities are `read`, `write`, `run`, `env`; omitted grants deny all four.
  An included grant with `names: []` (or omitted `names`) is **unrestricted** for
  that capability, not a denial. Filesystem roots resolve relative to call `cwd`.
- Python uses `{'read': ['./data'], 'run': True, 'env': ['HOME']}`. `True` emits an
  unrestricted grant; `False` or `[]` emits no option. Empty mappings do **not**
  disable CLI defaults: `km`/`expr` deny capabilities; `kmk`/`kash` and watches
  default to cwd read/write plus process execution. Explicit grant options replace
  those defaults. The wrapper has no separate deny-all-defaults option.
- Scoped run grants require explicit executable paths; legacy shell-text execution
  needs unrestricted run authority. Grants do not sandbox a child's internal I/O.

## JavaScript host requests

Omit `hostRequest` for built-in Node services. Otherwise the async callback handles
intercepted requests; there is no automatic fallback for an unhandled completion.
Its frozen request descriptor contains `id` (opaque BigInt handle), numeric `kind`,
string `payload`, copied `data` bytes, optional `key`/`record` bytes (or `null`),
`capability` (`read`/`write`/`run`/`env` or `null`), and cancellation `signal`.
For example, pass these options to `program.evaluate('(read "data.txt")', ...)`:

```js
{
  grants: [{ capability: 'read', names: ['data.txt'] }],
  hostRequest: async (request) => request.kind === 1 && request.payload === 'data.txt'
    ? { type: 'text', value: 'virtual data' }
    : { type: 'failure', code: 'HOST_FAIL', message: 'unsupported request' },
}
```

Return exactly one completion: `{ type: 'bytes', value: Uint8Array }`,
`{ type: 'text', value: string }`, `{ type: 'json', value: JSONValue }`,
`{ type: 'nil' }`, or `{ type: 'failure', code: string, message: string }`.
The expected completion depends on request kind; see the [WASM ABI](spec/010-wasm.md).
Denied requests do not reach the callback. Throws reject the request; late results
after cancellation are ignored, and an unresolved callback does not block disposal.
Python has no host-request callback API.

## Cancellation, watches, and cleanup

- JS operations accept `signal: controller.signal`; `controller.abort()` cancels
  work (`EXEC_CANCELLED`). Await `program.dispose()` or `kame.dispose()` in
  `finally`; both are idempotent and await owned cleanup. Later calls fail `DISPOSED`.
- JS `await program.watch(targets, options)` yields `{ snapshot, events }` batches.
  `watch.snapshot()` copies current state; after detecting changes, pass resource
  records from `snapshot.resources` to `watch.invalidate(resources)`. Keep consuming
  the iterator to drive work. Close the active watch before other program operations
  (`BUSY`). Always `await watch.close()` in `finally`, even after breaking iteration.
- Python watches require `kmk` and yield individual CLI JSON events, not snapshots;
  filesystem watching is performed by the CLI. There is no `invalidate` method.
  Always `await watch.close()` in `finally`; a loop break alone does not close it.
- Cancel Python request tasks with `task.cancel()` and await them; cancellation
  terminates/reaps owned processes (process groups on POSIX). `await program.close()`
  also removes temporary source files; `async with Kame(...)` closes all owned work.
  Failures raise `KameError` with `returncode`/`stderr`; JS errors expose `code`.
