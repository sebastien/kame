# Automatic clean and historical output ownership

## Purpose

Kame records artifacts it actually produces and offers an automatic clean
operation without requiring an authored removal recipe. Historical provenance
does not grant permanent permission to delete a path. Cleanup must renew that
permission against the current artifact, build context and protection state.

This contract refines [output ownership](039-output-ownership.md),
[runtime publication](006-runtime.md), [cache management](008-cache.md),
[inspection](037-build-inspection.md) and [CLI presentation](036-cli_ansi.md).
It does not add runtime-discovered output membership or sandbox opaque processes.

The default favors preserved stale artifacts over loss of user data. An unchanged
file can become important outside the build graph without any observable change;
Kame cannot infer that intent. Explicit protection is part of the contract, not a
substitute for validating the files that remain eligible.

## Command surface

```text
kame do clean [-C DIR] [--dry-run] [--json]
kame do clean [-C DIR] --keep PATH...
kame do clean [-C DIR] --release PATH...
```

`do clean` operates on the selected project's complete historical ledger, not
only outputs reachable from `default` or the current configuration. It does not
build prerequisites, start services or execute an authored recipe.

The bare `clean` target is supplied automatically only when no authored target
with that exact name exists. An authored `clean` retains ordinary target semantics;
Kame must not replace it, append automatic deletion to it, or execute it from
`do clean`. The explicit command remains available regardless of that declaration.

`--keep` persistently protects exact canonical paths, including paths not yet
present or recorded. Protection survives clean, rebuilds and configuration
changes. `--release` retires the existing ownership entry without removing the
file; it does not clear protection. A subsequent actual production may create a
new ownership entry, but cannot override a keep marker. These modes are mutually
exclusive and perform no cleanup. Paths are literal resources, never globs.

There is no blanket force option that bypasses validation. Artifacts withheld
because rules changed require explicit user review; the user may retain them,
release ownership or remove them independently. A review or dry-run result is
not a reusable authorization token for a later deletion.

## Persistent ledger

Ownership state lives under `.kame/outputs/`, separate from cache records. Cache
eviction and `do cache clean` must not erase it or remove recorded artifacts.
The directory and its records must not be followed through symlinks.

Each production entry contains:

- A versioned project identity bound to the selected physical project root.
- The canonical resource path and its validated root-relative member path.
- Producer declaration identity, complete captures and bound target arguments.
- Effective configuration and the ordered declared output set.
- A build-context signature and the material needed to validate it.
- The produced file kind, size, SHA-256 content digest and available host object
  identity and replacement/change observations.
- A production generation and a state distinguishing accepted publication from
  incomplete or uncertain attempts.

Content equality alone is not proof of object ownership. A detectable replacement
with identical bytes, kind change or metadata change inconsistent with the
production observation must withhold deletion. Modification time alone is never
proof of equality. A host unable to establish the required ownership observations
must report that limitation and preserve the artifact.

Entries accumulate across invocations, target selections and configurations.
Removing a rule, changing a pattern or shrinking a grouped output set does not
discard historical members. A path has one current accepted production entry;
successful later production replaces its previous observation atomically. Producer
changes remain visible in its provenance. Conflicting or ambiguous ownership
cannot authorize deletion.

Deleting or losing the ledger loses cleanup authority. Kame must not reconstruct
it by scanning directories, reading cache records, matching patterns or treating
existing declared outputs as previously produced files. Moving or copying a
project does not silently transfer deletion authority to its new physical root.

## Recording production

Record successfully published declared file outputs and explicit Kame writes
whose effects carry a concrete tracked destination. Yield publication follows the
same contract. Merely planning, reading, validating freshness or reusing an
existing artifact does not acquire ownership or update its production baseline.

For grouped outputs, validate every sibling before publishing the accepted
production batch. A failed batch cannot grant cleanup authority to partial files.
Ownership records must be complete, checksummed and atomically committed; malformed,
unsupported or partially written records never authorize removal.

Register an uncertain attempt before execution can modify a tracked destination.
If execution fails, is cancelled, crashes or cannot commit its ownership record,
the affected path remains uncertain, including when an older accepted entry
exists. Never fall back to that older entry as permission to delete the result.
A successful publication resolves its attempt marker. Recovery preserves
unresolved attempts for reporting; recording failure must be visible to the user.

Opaque shell side effects are not inventoried. A manifest is an ordinary artifact;
its listed members do not become owned. Globs declare input membership, not
cleanup membership. Directories do not imply ownership of their descendants.

An explicit tracked write can establish production provenance even when it
overwrites a pre-existing path; Kame cannot recover the overwritten data. Naming
a pre-existing path as an output without executing a producer never establishes
such provenance. Keep markers prevent cleanup, not build-time overwrites.

## Build-context validation

Automatic deletion requires the recorded build context to remain equivalent.
The signature covers all loaded authored build sources and includes, their
membership and contents, generated declarations, effective configuration,
producer/capture/argument selection, output membership, and definitions or host
observations that determine those declarations and destinations. Changes to
recipe bodies are changes to this context, even if output paths remain identical.

Use exact authored-source content for this conservative guard; formatting-only
edits may withhold cleanup. Persist enough source identities to detect removed,
added or unavailable sources. A different entry point or configuration does not
validate an older production merely because its path still matches a producer.

Validation is read-only and must not materialize prerequisites or run generators
or processes. Contexts requiring effects or unavailable historical configuration
to establish equivalence are unverifiable. Preserve their artifacts rather than
guessing. Cache freshness guards may supply evidence but cannot replace this
ownership contract.

A changed context withholds automatic deletion of affected historical entries.
In particular, the absence of a current producer is not evidence of disposal:
the artifact may now be an external input or hand-maintained source. A later
no-op build must not rebaseline it to the new rules and make it deletable.

Protect currently loaded build sources and include files, explicit keep paths,
and resources established as external inputs. Generated intermediate inputs may
remain eligible only when their current production ownership is verified.
Inspection of `default` alone cannot prove safety. Unresolved computed inputs,
uninstantiated patterns and opaque process reads are completeness boundaries,
not evidence that a historical artifact is unused. When a boundary prevents a
required safety decision, preserve the candidate and explain why.

## Eligibility and filesystem boundary

The following checks apply to each recorded member:

| Observation | Result |
| --- | --- |
| Missing member, established through a safe root-relative lookup | Resolve entry; remove nothing |
| Unchanged owned regular file, equivalent context, no protection and safe host operation | Eligible for removal |
| Modified, replaced, protected, uncertain or conflicting artifact | Preserve entry and file |
| Changed, missing or unverifiable build context | Preserve entry and file |
| Unsafe path, corrupt record, denied operation or unavailable host guarantee | Refuse removal and report error |

Absence must be established, not inferred from permission errors, unreadable
parents or failed host requests. Missing-file retirement never follows symlinks
or an unchecked path from a damaged record.

Automatic cleanup is restricted to regular files strictly inside the recorded
project root. Never remove the root, ownership state, a directory, a symlink,
a special file or an out-of-root resource. Reject traversal, canonical aliases
that escape the root and host-specific path ambiguity. Validate containment
against actual host lookup semantics, including case handling and parent links,
not just string prefixes. No directory pruning or recursive removal is implied.

The host must provide a race-resistant root-relative cleanup operation that
binds validation and removal to the intended member and prevents parent-link
substitution or deletion of a replaced object. A portable evaluator sequence of
`lstat`, hash and ordinary path unlink is insufficient. If the host cannot uphold
this guarantee, cleanup reports an unsupported safety boundary rather than
weakening it. Native, Windows and forwarded WASM hosts share this requirement;
their supported mechanisms need not be identical.

A host may implement the operation by atomically capturing a public member into
a private, descriptor-relative recovery name, then validating the captured object
before removal. Persist its original member path and recovery receipt before the
capture. If validation fails, restore without overwriting a newly occupied public
name; when restoration is impossible, retain the captured object and report its
recovery path. Never purge an unvalidated captured object, including a symlink
or directory substituted during the capture. Interruption can leave a public path
temporarily absent, but must leave recoverable bytes and explicit ownership state.
An outstanding recovery receipt blocks missing-member retirement and automatic
ownership release. Recovery is a user-visible safety boundary, not successful
cleanup or a reason to recursively remove staging contents.

Serialize cleanup against cooperating Kame builds, tracked writes and ownership
updates for the same project. Watch invocations must quiesce their producers or
make cleanup fail as busy; they must not immediately race to rebuild removed
outputs. Revalidate under the protected cleanup operation, not only during a
previous dry-run. Locking Kame does not lock editors or arbitrary external tools.

No guarantee is made against malicious or uncooperative writers modifying a
file through an already open handle during cleanup, or tampering with trusted
project metadata. Detectable external changes must withhold removal; the command
must not describe project locks or content hashes as universal race protection.
Read and removal permissions remain subject to ordinary host capability policy.

## Progress, interruption and retention

Dry-run applies the same eligibility checks without deleting files, retiring
entries, changing protection or rebaselining production observations. It reports
an inspection snapshot, not a promise that the next invocation removes the same
set. Normal cleanup reports actual results.

Retire an entry only after successful removal or established absence. Preserve
all withheld entries and uncertain attempts for later inspection. Cleanup does
not clear the entire ledger or advance a global cutoff that forgets unresolved
outputs. Thus the historical set is everything produced since its last resolved
cleanup, plus unresolved artifacts carried forward from earlier cleans.

Removal of multiple members is not an all-or-nothing filesystem transaction.
Commit progress durably per member or recoverably per batch. A crash between
removal and retirement may leave an entry for a now-missing file; retry safely
resolves it. A crash must never retire an entry for a file not actually removed
or established absent. Partial clean is retryable and does not touch unrelated
members.

Human and JSON output identify each canonical path, its provenance where
available, and one of `removed`, `missing`, `would-remove`, `preserved` or `error`,
with a stable reason. Summaries count actual removals separately from preserved
and missing artifacts. Denied or unsupported host operations, corrupt state,
busy projects and cancellation produce nonzero exit status. Safety-preserved
artifacts are not command failures, but must be conspicuous in the summary;
success does not mean every historical artifact disappeared. JSON carries the
same decisions and reasons without process or recipe events.

## Acceptance criteria

- Multiple invocations, captures and configurations accumulate produced outputs;
  cleanup is not limited to the default target's graph.
- Freshness reuse and cache hits do not acquire ownership of pre-existing files
  or refresh historical production observations.
- Successful unchanged production can be cleaned; byte edits, metadata changes,
  same-byte detectable replacement and kind changes preserve the file.
- Rule removal, recipe edits, include membership changes, configuration changes
  and grouped-member removal preserve historical artifacts pending review.
- An output becoming an external input or build source is preserved, including
  when its bytes did not change. A no-op build cannot erase that boundary.
- Keep markers protect missing and present paths across later production. Release
  retires ownership without touching bytes and does not clear protection.
- Untracked shell side effects, manifest members, glob matches and unrelated
  files sharing an output directory are never inferred as cleanup members.
- Partial grouped publication, failed writes and interrupted record commits
  remain uncertain and cannot use an older accepted record to authorize removal.
- Cache cleaning preserves the ledger. Missing, corrupt or copied ownership
  state never triggers directory scanning or silent authority reconstruction.
- Unsafe roots, parent symlink substitution, final symlinks, outside-root paths,
  permission failures and unavailable race-resistant host operations remove no
  affected files and produce explicit diagnostics.
- Cooperating concurrent builds and watch producers cannot race cleanup.
  Dry-run decisions are revalidated during actual removal.
- Interrupted partial cleanup retains unresolved entries and is safely retryable;
  a second clean resolves previously removed members without deleting replacements.
- Authored `clean` tasks retain their semantics; the explicit automatic command
  neither invokes nor augments them. Native and WASM reports have equivalent
  decisions, reasons, counts and exit status for equivalent host capabilities.
