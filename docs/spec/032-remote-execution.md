# Remote execution

## Purpose

An explicitly selected executor may run a file rule away from the local
host while preserving Kame's declared dependency graph, process events,
cancellation and output publication. Executor selection is portable policy;
network configuration and credentials belong to the embedding host.

## Selection and fallback

A file rule may declare `[executor: "local"]` or
`[executor: "remote:NAME"]`. An omitted executor means `local`. The remote
name is a stable, case-sensitive identifier registered by the embedding host.
Task rules and service rules remain local in this version because they do not
declare a closed set of output artifacts.

There is no implicit remote selection and no fallback from a requested remote
executor to local execution. If the named executor is not configured, fail
with `FEATURE_UNSUP` before starting a process. Executor identity and version
are included in task fingerprints and cache records; changing a backend
cannot reuse results produced by a different one.

## Request and artifact transport

The portable `host.ProcessRequest` contains a request ID, executor name and
version, target identity, generation and attempt, a retry-stable idempotency
key, recipe script or argv stages, working directory, timeout, the exact
permitted environment, declared input artifacts and declared output keys. A
`host.ProcessHost` advertises support for an executor name and version before
Kame stages inputs or starts it. `ExecutorDescriptor` values register the
names and versions available to a compiled program. Local hosts advertise only
the local executor unless they provide a remote adapter.
The current structured forwarded-process wire format does not carry remote
artifact manifests, so a remote selection through that path fails with
`FEATURE_UNSUP` instead of being run locally.

Each `host.ExecutionArtifact` input carries its workspace-relative path (or
canonical resource URI), bytes, mode and SHA-256 content digest. Inputs are
copied into request-owned storage before submission. Remote workspaces use a
stable workspace-relative layout; host-local absolute paths are not required in
artifact fields. The request's directory is `.` for remote execution.

Only declared file-rule inputs are uploaded. A missing input fails before
submission. The executor may read additional files from its isolated workspace
only when they are present in the request manifest. The executor returns
bytes and mode for every declared output. Kame reports a missing declared
output as `OUTPUT_MISSING` and duplicate or undeclared outputs as `HOST_FAIL`,
then publishes validated outputs through the local host's atomic write API
only after successful process completion. Process failures and cancellation
publish no output bytes; a local publication failure reports `FS_ERR`. The
terminal `host.ProcessEvent` carries output artifacts, and published
permissions are restricted to the ordinary `0777` bits.

File and resource URI identity remains canonical across transport. Memory URI
inputs are serialized as their canonical resource keys with their contents;
output URIs are written back through the configured host. Input/output grants
are checked by Kame before transport, and an executor cannot expand them.

## Events, status and cancellation

The executor contract emits ordered stdout and stderr chunks followed by one
terminal event containing outcome, exit status, signal and a bounded captured
prefix for each stream. Process-started and terminal events retain the
existing Kame request identity. Cancellation names the original request ID,
stops remote work, and produces one terminal `EXEC_CANCELLED` result. A late
event for an invalidated generation is ignored. Retries use a stable
idempotency key derived from target identity, generation-independent inputs
and executor identity; a retry must not duplicate output publication.

## Secrets and environment

Remote requests never include the ambient parent process environment. They
include only literal rule-scoped `env:` assignments resolved for that target.
All other environment names and values are absent. The remote adapter owns
its credentials and secret lookup; credentials are not fields in the request,
cache key, task diagnostic, process stream or result event. Kame does not
upload undeclared files to discover credentials or environment values.

## Local and remote conformance

The local executor and a deterministic fake remote executor consume the same
request shape and produce the same successful artifact bytes, ordered stream
events and terminal status for equivalent commands. The fake remote executor
also exercises non-zero exit, missing and undeclared outputs, stream
truncation, timeout, cancellation, late completion after invalidation,
missing executor selection, scoped environment filtering and allocator cleanup.
Tests prove that missing and unregistered executors fail closed, successful
requests carry the declared artifacts and only rule-scoped environment, missing
or undeclared outputs are rejected, failed results publish no outputs, timeout
and cancellation publish no artifacts, retries reuse an idempotency key, and
truncation metadata survives terminal events. The publication test reads the
result back through the host filesystem contract. Input grants are checked by
the existing program planning gate before `prepareRemoteExecution` reads or
stages any input.
