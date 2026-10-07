# Plugin operations

## Purpose and boundary

Explicitly loaded operations run through a trusted native plugin
process or a JavaScript host callback. Plugins extend the operation registry;
they do not add syntax, definitions, rules, or host capabilities. Kame values
cross the plugin boundary as bounded canonical JSON. Go pointers, allocator
handles, evaluator contexts, and callbacks never cross it.

Plugins are loaded by the embedding application or CLI configuration. There is
no directory scanning, implicit discovery, network install, or automatic
fallback. A plugin is trusted code: native executables run with the authority
of their configured host process, and JavaScript callbacks run with the
authority explicitly given by their host.

## Declaration and registration

A plugin declaration contains:

- A stable plugin name and nonempty implementation version.
- An explicit native executable argv, or a JavaScript callback registration.
- One or more operation declarations with stable names and nonempty versions.
- Minimum and maximum arity, required Kame capabilities, and per-call input,
  output, and time limits.

Names are unique within a registry. Plugin loading is all-or-nothing: malformed
or duplicate declarations reject the configuration before evaluation. The
declared operation names are installed in `eval.Registry` with normal arity and
capability checks. An unavailable or disallowed plugin fails with
`FEATURE_UNSUP` before its handler is invoked. Operation fingerprints include
plugin name, plugin version, operation name, and operation version.

Plugin operations cannot claim a Kame capability that the caller lacks. The
evaluator checks both the operation declaration and the invocation's grants
before constructing a host request. Plugin results do not create implicit
filesystem dependencies; operations that read or write files must request the
corresponding host operation through Kame's existing capability boundary.

## Value protocol

Each call is one UTF-8 JSON object containing protocol version, plugin and
operation identity, request ID, call generation and attempt, and a positional
array of canonical Kame values. A successful response contains the same
protocol and request identities and exactly one canonical Kame value. A failed
response contains a bounded stable error code and message and no value.
Unknown fields, wrong field types, malformed JSON, mismatched identities, and
invalid Kame values fail with `PLUGIN_PROTOCOL`.

The request limit is 1 MiB, the result limit is 1 MiB, and the default call
timeout is 30 seconds. Declarations may lower these limits; hosts may impose
stricter limits. Exceeding either byte limit fails with `PLUGIN_LIMIT` before
the value is published. Calls return one value in this version; plugin-created
streams and nested callbacks are unsupported.

The canonical Kame JSON value codec is used without interpreting strings as
source code. Bytes, patterns, lists, records, nil, booleans, integers and
floating point values retain their normal tagged encoding and validation.
Input values are copied for the host request. Response values are decoded into
Kame-owned storage before completion, so plugin buffers may be released as
soon as the response is accepted.

## Native process adapter

The Node.js adapter starts the configured argv for each invocation, writes one
request object followed by a newline to stdin, and reads one response object
from stdout. It drains and discards stderr so plugins cannot block on a full
pipe or expose private output through diagnostics. The adapter closes stdin
after the request, rejects extra stdout records or a nonzero exit, and reaps the
whole child process group on completion, timeout, cancellation, or embedding
disposal. The executable path and argv are configuration data and are never
interpolated into a shell command.

## JavaScript host adapter

The Node.js embedding accepts declarations in `plugins` and an optional map of
plugin names to callbacks in `pluginCallbacks` on each evaluate, build, or
watch invocation. A declaration may provide `argv` for the native-process
adapter; an explicit callback for the same plugin takes precedence. The process
adapter passes argv directly to `spawn` with shell parsing disabled, writes one
JSON request line to stdin, and accepts exactly one JSON response line from
stdout. It drains but never exposes stderr, bounds stdout, and terminates the
child process group on timeout, cancellation, or embedding disposal. A callback
receives an object with `protocol`, `request`, `plugin`, `pluginVersion`,
`operation`, `operationVersion`, `generation`, `attempt`, and a positional
`args` array containing tagged Kame values. It also receives `{ signal }` as
its second argument. The response repeats all request identity fields and
contains exactly one tagged `value`, or an `error` with a stable code and
bounded message. Unknown fields or mismatched identity fail with
`PLUGIN_PROTOCOL`.

Callback results may be promises. Rejected promises become `PLUGIN_FAIL`;
expired calls become `PLUGIN_TIMEOUT`. The runtime aborts the callback signal
on cancellation, timeout, or terminal completion, copies the response before
resuming Kame, and ignores completion after cancellation or generation
replacement. Request and full response envelope sizes are checked against the
declared bounds.

## Failure, cancellation, and ownership

`PLUGIN_FAIL` reports a plugin failure without including request values,
response values, stderr, credentials, or ambient environment in diagnostic
causes. `PLUGIN_TIMEOUT` identifies the timeout without exposing captured
output. Cancellation stops the native process group or marks the JavaScript
request cancelled; late results are ignored. Each request has one terminal
completion. Request and response buffers, child processes, and pending
callbacks are released on success, failure, timeout, cancellation, and runtime
disposal.

## Acceptance

- Native and JavaScript adapters round-trip every canonical Kame value kind.
- Both adapters reject malformed, oversized, mistyped, wrong-version, and
  wrong-request responses with stable diagnostics.
- Duplicate plugin or operation declarations fail before evaluation.
- Missing or disallowed plugins fail before callback/process invocation.
- Arity and capability denials happen before any plugin side effect.
- Operation identity changes when either plugin or operation version changes.
- Native argv executes without shell interpolation and receives only the
  serialized call on stdin.
- Nonzero exit, callback rejection, timeout, cancellation, stale completion,
  and disposal each produce one terminal result and clean up their resources.
- Repeated bounded calls leave no allocator-owned values, child processes, or
  pending JavaScript callbacks behind.
