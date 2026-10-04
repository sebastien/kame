# Managed service rules

## Scope

A `service` rule starts and supervises one long-running process as a shared
program node. Its declared prerequisites are provisioning work: they must finish
before startup and remain interested for the full service lifetime. A service
root remains active until its caller releases it; a dependent keeps the service
alive until that dependent terminates. Ordinary `task` and file-rule completion
semantics do not change.

The service recipe body is the start command. It runs under the rule's existing
shell, environment, grant, cwd, and authored-source policy. A service body may
emit bounded stdout and stderr, but cannot yield file content, publish outputs,
or use cached-task identity. It is valid for the process to run indefinitely.

## Typed configuration

The existing rule metadata record accepts the following additional service-only
fields:

```kame
service api : ./bin/api ; [
  ready: [argv: ["./bin/api" "ready"] interval-ms: 100 timeout-ms: 15000]
  health: [argv: ["./bin/api" "health"] interval-ms: 1000 failures: 3]
  restart: [attempts: 3 backoff-ms: 250]
  stop: [grace-ms: 5000]
  log-bytes: 65536
]
	./bin/api serve
```

The top-level fields are `ready`, `health`, `restart`, `stop`, and `log-bytes`.
Unknown fields, duplicate keys, wrong types, out-of-range numbers, empty argv,
NULs, and service-only fields on other rule kinds fail before prerequisites or
processes run.

`ready` is optional. Without it, a service becomes ready when its start process
has spawned. When configured, `argv` is executed directly (without a shell) at
the configured interval until it exits successfully or `timeout-ms` elapses.
Its default interval is 100 ms. Probe output is discarded. `health` is optional;
when configured, its direct `argv` probe runs at the configured interval, which
defaults to 1000 ms. `failures` consecutive unsuccessful probes mark the service
unhealthy. Probe intervals and counts must be positive and bounded.

`restart` defaults to zero retries and zero delay. `attempts` is the maximum
number of restarts after a failed start, readiness timeout, unhealthy result, or
unexpected process exit. `backoff-ms` defaults to 250 ms and is bounded. A
restart repeats prerequisite freshness checks before launching the body again.
Consumers wait while the service is restarting and resume only after readiness.
Exhausting retries fails the service with a stable service diagnostic; process
output is never copied into that diagnostic.

`stop.grace-ms` defaults to 5000 ms. Shutdown sends SIGTERM to the service
process group, waits for the grace period, then sends SIGKILL and reaps every
child. `log-bytes` defaults to 65536 bytes per stream. Retention is bounded per
service and reports truncation in service events.

All metadata is evaluated under planning policy before any prerequisite recipe
runs. Service `env` and `shell` metadata retain their existing meanings; the
health and readiness probes inherit the resolved service environment, while
their argv comes only from the typed configuration.

## Lifecycle and ownership

The portable service state is `provisioning`, `starting`, `checking-ready`,
`ready`, `checking-health`, `restarting`, `stopping`, or terminal. The runtime
owns the service node, process identity, timers, restart count, probe request,
and bounded log buffers. Native and WASM hosts own child processes and process
groups; the portable runtime owns lifecycle decisions.

The service node publishes its ready value only after the configured readiness
condition succeeds. Dependents cannot start before that publication. Health
checks continue while the node has interest. An unhealthy or exited service is
removed from the current graph before dependents are resumed, so consumers
cannot observe stale readiness. A successful restart publishes a new service
revision and resumes the retained dependency graph.

When the last root or dependent releases a service, cancellation begins graceful
shutdown. Prerequisite interest is retained until the service process and its
children have been reaped, then released. Failed startup, failed probes,
readiness timeout, caller cancellation, host failure, and program disposal all
use the same teardown path. Late process or probe events are rejected by service
generation and request identity.

Service lifecycle events identify target, generation, attempt, and state. They
never include environment values or unbounded process output. Logs are emitted
as bounded stdout/stderr events and retain independent per-stream truncation
markers. JSON and human output report the same lifecycle transitions.

## Host contract

Native and WASM hosts expose start, direct-argv probe, signal, timed wait, and
reap operations for a service process group. Start returns a stable process
handle and a spawn event before readiness checks begin. Stop is idempotent. A
host must retain service stdout and stderr only up to the configured bound and
must terminate descendants on cancellation or teardown.

The WASM module does not spawn, probe, signal, or read clocks. It emits typed
service requests through the existing embedding boundary and accepts correlated
start, probe, timer, and terminal completions. The JavaScript CLI implements
those requests with Node child processes. Public embedding callers must receive
the same lifecycle events and must be able to cancel and dispose a service
without leaving a process group alive.

## Acceptance

- Configuration validates completely before provisioning prerequisites or
  executing any process; native and WASM diagnostics preserve authored spans.
- Prerequisites run before startup, are shared by concurrent consumers, and
  remain live until the service stops.
- Readiness gates dependents; timeout and failed probes follow the restart policy.
- Health failure and unexpected exit restart within bounds, then fail consumers
  cleanly after retries are exhausted.
- Releasing one consumer does not stop a shared service; releasing the last
  consumer performs graceful stop, forced escalation when needed, and reap.
- Logs remain within the configured per-stream bound, report truncation, and do
  not leak into diagnostics.
- Cancellation, timeout, host failure, invalidation, and instance disposal stop
  probes and process trees and reject late completions.
- Native, WASM CLI, and public embedding tests cover lifecycle parity, repeated
  start/stop, shared prerequisites, restart recovery, health failure and cleanup.

## Implementation status

The runtime currently starts a service body as a persistent process and
publishes the default spawn-ready value, allowing dependent rules to proceed
while the process remains active. Releasing the final dependent cancels it.
Typed readiness and health probes, restart policy, grace-period shutdown,
bounded service log retention, explicit root-ready handles, and lifecycle event
parity remain open; configured probes or restarts still report
`FEATURE_UNSUP`.
