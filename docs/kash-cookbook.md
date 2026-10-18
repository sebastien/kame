# Operational Kash cookbook

Use a build from this checkout, not merely a binary with the same version label.
Run `make build/kame.debug dist-wasm`, then use `build/kame.debug` or
`node dist/kame.js` in place of `kame`. Pipeline/routing syntax and atomic
publication of externally generated artifacts are separate work.

The harmless, complete example is
[`examples/shell/04-operations.kash`](../examples/shell/04-operations.kash).
Run it from a disposable writable directory. It creates a private workspace and
removes it with `defer`; no credentials, installation or network are involved.

## Typed prefixes, not generated command source

```kash
prefix = []
command = (concat prefix ["printf" "<%s>" "two words" ""])
$...command tail
```

Compose an optional empty prefix with the actual executable first: the final
command list must not be empty. A prefix may contain a wrapper and its arguments.
Spreading preserves empty arguments, quotes, newlines and spaces, rejects nested
lists and never interprets argument bytes as source. CLI callers can supply
closed list data with `--set`, not joined shell strings.

Raw `*.c` is literal; `(wildcard ./src/*.c)` explicitly produces an argv list.
Lists splice only as standalone unquoted words. Quoted/mixed substitutions must
be scalar. `${name}.suffix` appends text; `$name.suffix` performs field lookup.
Double quotes group words and still allow references/capture; single quotes are
not Kash quoting. Parenthesized expressions use Kame values and value pipes,
not shell grouping.

## Accept selected statuses only after explicit `?`

```kash
attempt = (kash """$...args ?""")
result = (attempt "tidy" "-o" "./staged.html" "./input.html")
(if (and (= result.signal 0) (has? result.status [0 1]))
  :nil
  (fail! "Tidy failed; do not publish its output"))
```

The callable is authored code, not generated command text. Args remain typed.
Without `?`, an unsuccessful command stops execution. With `?`, inspect its
existing completion record. This example rejects status 2 and signals; missing
tools, timeouts and denied capabilities still fail. A nonempty output file is not
evidence of success.

`CMD ? FALLBACK` is explicit process recovery; fallback failure still fails.
Terminal `CMD ?` starts no fallback. Bare `nop` names an external executable,
not Kame's `(nop)`. `LEFT ?? RIGHT` is Kash value recovery, not an expression
operator or finalizer. Cancellation/capability denial cannot be swallowed.
Command conditions test exit status; expression conditions use Kame truth.
Only nil and false are false; zero and empty strings/lists are true.

Strict pipelines still count every stage, including SIGPIPE. Do not infer success
from the final stage or weaken checks to hide a failed downloader. Future
early-close policy and routing syntax are separate from these changes.

## Finite work and bounded retry

```kash
(map ([file] (run! "checker" file)) files)
```

Callbacks run in order and stop on failure. To collect failures, map an authored
callable whose command uses `?`, then inspect every result. Use `reduce` with a
small accumulator when retaining all callback results is unnecessary.

```kash
probe = (kash """$...args ?""")
result = (reduce ([previous number]
  (if (= previous.status 0) previous
    (if (and (= previous.signal 0) (= previous.status 75))
      (probe "safe-idempotent-probe" number)
      (fail! "non-retryable failure"))))
  [status: 75 signal: 0] [1 2 3])
(if (= result.status 0) :nil (fail! "retry budget exhausted"))
```

Only the explicitly chosen transient status is eligible; success stops new
attempts. Each call is fresh. Repeatedly referencing one lazy result is not
retry; an unused definition has no effects. Suspension resumes without replay.
CLI recipe retry is a different policy. Never apply either policy to deployments
without explicitly establishing that repeating the operation is safe.

## Temporary workspaces and `defer`

```kash
created = $(mktemp -d ./workspace.XXXXXX)
workspace = (if (ends? "\n" created) (slice 0 -1 created) created)
defer rm -rf $workspace
```

`mktemp` promises one framing LF here: remove only that LF, not arbitrary path
whitespace. Command registration demands/snapshots the workspace path. Demanded
top-level lazy values survive later statements and finalization. The directory
is fresh and private: never substitute a shared directory, glob or existing path.

`defer (cleanup workspace)` evaluates the whole expression at cleanup, retaining
its lexical scope. An undemanded definition stays lazy until then. Branches share
their invocation's stack; nested Kash callables have independent stacks. Actions
run LIFO after user work joins, or after failing/cancelled work is reaped.
Cleanup-started async work also joins. Deferred commands cannot use `&` or direct
fallback: wrap compound recovery in an authored callable and defer its invocation.
Terminal `?` remains available.

Cleanup failure does not mask an existing failure. Remaining actions are tried
within a separate five-second stack budget; tighter process timeouts still apply.
SIGKILL/crash/forced shutdown can prevent cleanup. Blocking custom hosts are not
preempted by a process timeout. Finalization retains the original grants.

This attempts authored cleanup; it is not ownership enforcement. It does not make
`tool > ./final` atomic, restore a truncated file, or reverse remote changes.
Generate into staging and publish explicitly after success. Atomic publication
of external artifacts is follow-up work; `do clean` is not a finalizer.

## Environments, captures and tool lookup

```kash
exports = [TOKEN: $(credential-reader) MODE: "preview"]
:env $exports application
```

Capture is exact UTF-8: empty output, spaces, CRLF and every terminal newline
survive. Normalize only when the producer specifies framing. The default capture
limit is 1 MiB; overflow/invalid UTF-8 fail without successful truncated values.
Stderr remains live. Stream large/binary payloads to files, not captured strings.

Environment names must be valid and values must be strings without NUL. The whole
graph is validated before launch. Computed values are omitted from normal errors
and lifecycle traces, but authored literals, argv, explicit output and child
output can expose secrets. Environment delivery does not hide values from
authorized processes or OS observers. Do not generate exports files or strip
credentials by default.

Three separate boundaries:

1. `$NAME` is a Kame binding. `$env.NAME` explicitly reads the environment and
   needs an env grant; delivering a child environment grants no reads.
2. Bare tools use startup PATH and `--tool NAME=PATH`. Explicit executable paths
   use stage cwd. A stage PATH override changes the child, not Kame's resolver.
   `TOOL_MISSING` differs from `CAP_DENIED`.
3. A wrapper or child shell can do its own lookup. Tracking a wrapper does not
   discover its packages/scripts/subprocesses. Declare relevant file inputs.

When a vendor needs sourcing or an interactive shell, launch an explicitly
authored external shell. Do not interpret its stdout as Kash source.

## Documents are data

```kash
(write! ./literal.txt """Literal $HOME and @(not-a-call).""")
template = """Hello, @{name}.
"""
(write! ./message.txt (render [name: "Ada"] template))
(write! ./config.json (json [name: "a\"b" lines: ["one" "two\nthree"]]))
consumer < ./config.json
```

Verbatim text remains literal until explicitly rendered. JSON escaping belongs
to `json`, not shell quoting. File-backed rendering and input redirection track
reads; arbitrary filename arguments do not automatically declare dependencies.
`write!`/`yield!` retain their phase/publication contracts, not a promise of a
transactional multi-output external recipe.

## Evidence and debugging boundaries

Linux native and WASM/Node checks include:

| Contract | Executable checks |
| --- | --- |
| Prefixes and tool provenance | `T038-01-kash-direct-recipes.sh` |
| Environment scope/grants/traces | `T017-20-kash-context-environment.sh`, `T017-23-kash-operations.sh` |
| Exact capture, encoding and bounds | `T017-02-wasm-capture.sh`, `T017-23-kash-operations.sh` |
| Invocation cleanup, errors and reaping | `T017-22-kash-defer.sh` |
| Background joins and failures | `T017-18-kash-async.sh` |
| Streaming and slow-consumer pressure | `T017-03-eval-pipelines.sh`, `T012-04-streams-backpressure.sh` |

Parser success is not runtime evidence. These tests do not newly certify other
platforms: Windows job-assignment and non-Linux APE descendant-retirement
limitations remain explicit in the native host source.

For nonsecret debugging, print typed argv using `json` and retain the authored cwd
and environment *names*. Lifecycle displays may be truncated and are not shell
replay commands. Never dump computed credential environments. Unsupported syntax
must fail at an authored location, never silently fall back to a system shell.
