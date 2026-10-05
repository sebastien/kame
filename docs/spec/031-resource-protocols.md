# Resource protocols

## Purpose

D13 adds explicit resource URIs to Kame's filesystem-oriented build model. It
keeps relative and absolute paths working as they do today while allowing a
program to name a resource independently of its host representation. The first
release supports `file:` and `mem:` protocols through the existing portable
host boundary.

## URI syntax and canonical identity

A resource URI is `scheme://authority/path`. Scheme names are lowercase ASCII
and limited to `file` and `mem` in this release. URI paths use `/` separators;
percent escapes must be valid UTF-8 byte escapes and are decoded before path
normalization. Query and fragment components are rejected. Empty path segments
and `.` segments collapse; `..` may not escape the protocol root. Authority is
empty for `file:` and a non-empty, case-sensitive namespace for `mem:`.

`file:///absolute/path` names an absolute filesystem path. `file://` with a
non-empty authority is invalid. Existing relative and absolute path inputs
continue to identify filesystem resources and keep their current path rules;
they are not silently interpreted as URIs. Canonical identity is the tuple
`(scheme, authority, normalized path)`, so equivalent spellings share one
reactive resource node and cache identity. Display retains the canonical URI.

`mem://NAME/path` names an entry in a host-provided, namespace-scoped memory
filesystem. The host owns contents and mutation; Kame owns URI parsing,
identity, dependency edges, access checks, and invalidation. A portable memory
host must implement stat, read, directory listing, atomic write, mkdir, remove,
and deterministic modification times. It is a concrete protocol adapter, not a
process-global store.

## Language and dependency behavior

`(resource URI)` parses and validates a URI and returns a resource value. Invalid
syntax or an unsupported scheme is `RES_INVALID` at the URI expression. A
resource value may be used wherever a file resource is accepted, including
rule inputs and dynamic dependencies. Existing string paths keep their current
meaning; callers opt into protocol identity by constructing a resource value.

Planning and execution record canonical resource keys. Reads, stats, directory
listings and globs register dependencies against the canonical key. A protocol
change invalidates dependent plans, generated declarations, file contexts and
watch roots. Resource keys are preserved in plan output, diagnostics and JSON
lifecycle events without exposing host-local paths for non-file protocols.

Globs over `file:` resources preserve sorted traversal and existing wildcard
semantics. `mem:` directory listings and globs use the host's stable entry
order before Kame applies the same canonical sort. Cross-protocol path joins
are invalid; relative joins remain within their original authority and may not
escape the root.

## Grants and errors

Resource access is checked after canonicalization and before host calls. File
URIs use the existing filesystem read/write grants and path-prefix checks.
Memory URIs use those same capabilities with URI roots, for example
`--allow-read=mem://NAME/` and `--allow-write=mem://NAME/`; granting one
namespace does not grant another. An unrestricted read/write grant applies to
all protocols. Read, write, list, stat and glob operations each request the
matching capability. Planning must not perform writes. Errors identify the
canonical resource URI and operation while leaving host errors opaque.

The host protocol interface reports missing resources separately from host
failures. A missing entry remains an ordinary absent dependency; malformed
URIs, denied grants, unsupported protocols and host failures produce stable,
source-located diagnostics. Cancellation and host failure release all copied
URI and payload storage.

## Acceptance

- Valid and invalid URI syntax, percent escapes, root traversal, authority and
  normalization have source-located tests.
- Equivalent file and memory URI spellings have equal resource identity and
  cache keys; different memory namespaces remain isolated.
- A portable in-memory host supports read, stat, list, atomic publication,
  removal and deterministic watch invalidation through the standard host API.
- File URI behavior matches existing path behavior for grants, dependencies,
  glob ordering, build freshness and cache identity.
- Memory resource dependencies invalidate on add, replace and remove; native,
  WASM CLI and embedded runtimes agree.
- Missing resources, denied grants, malformed URIs, host errors and cancellation
  release allocations and do not publish partial outputs.
