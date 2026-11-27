---
revision: 2
---

# Kash history

## Purpose

Terminal Kash sessions retain useful command history without indefinitely
retaining credentials or unrelated project commands. This specification refines
the history contract in [052 Kash interactive](052-kash-interactive.md) and takes
precedence for history persistence, filtering, recall, and configuration.

History is local plaintext, not a credential vault. Secret detection is
best-effort: it cannot prove that an entry is safe. Expiration and deletion
remove entries from Kash's active storage, not filesystem snapshots, backups,
terminal scrollback, or another program's logs. Kash does not promise secure
erasure from physical media.

## Entries and scopes

An entry contains a unique identity, UTC submission timestamp, complete authored
command text, canonical absolute submission CWD, and canonical scope root and
kind. Multi-line input is one entry. Capture context before executing the entry:
a command that changes directory belongs to the directory it was submitted in.
Do not persist expanded variables, captured output, or execution results.

The owning scope is determined at submission:

- Walk from the canonical CWD toward the filesystem root. The nearest ancestor,
  including CWD, containing a `.git` file or directory or a `.jj` directory is
  the repository scope root.
- A Git worktree uses its worktree root, not the shared Git metadata directory.
  Co-located Git/Jujutsu metadata at the same root identifies one repository
  scope. Nested repositories select the nearest root.
- Without a repository marker, the canonical CWD is the directory scope root.
- Do not execute repository tools, read repository configuration, or traverse
  repository metadata to establish scope. Failure to establish a canonical CWD
  prevents recording that entry rather than assigning it to an unrelated scope.

Repository detection supplies context, not a security or access-control boundary.
Entries are not reassigned when directories move or repository markers change.

## Recall views

The default view is `local`: entries owned by the current repository/CWD scope.
Recompute the current scope before each prompt, so directory changes immediately
affect recall. `global` is the chronological union of all retained scopes; it
does not create copies or a separate global persistence scope.

`history` lists the selected view, oldest first. Its first line reports the
view and, in the local view, the current scope root; it ends with hints for the
other view and Ctrl-R search. `history -l|--local` and `history -g|--global`
select and list that view; the initial view is local. These are forms of the
interactive `history` builtin (`055` §6), not executables or Kame functions;
the builtin is `PARSE_ERR` in a script or rule body. They are not themselves
recorded. Switching views preserves unsubmitted input and changes both
navigation and incremental history search. Listing and recall never execute an
entry.

Tab completes the view flags and `clear`, then `local` or `global` after
`clear`.

Ctrl-X h toggles local/global recall while editing, preserving the input buffer
and cursor. The editor must invalidate its own cached recalled edits when scope
or eligibility changes; filtering only the persistence source is insufficient.

Within Ctrl-R incremental history search, Alt-G switches scope without leaving
search or clearing the query. The search label shows the current scope and
matches show their submission time in the local timezone. The timestamp is
display-only; it is not searched or inserted into the command. To rebind
Alt-G in `INPUTRC`, select `set keymap isearch` and bind
`kash-history-search-toggle` to the desired key.

The editor retains at most 1000 recent eligible entries in the selected view.
This is an in-memory recall bound, separate from persistence quotas.

Refresh the store before each prompt and view change. Other sessions' additions,
expiration, and deletions become visible at that boundary; live updates while
editing are not required. Recheck eligibility before presenting a recalled
entry, including while a prompt remains open beyond the retention deadline.
Already authored or recalled text in the editing buffer is not erased.

## Eligibility and secrets

Apply eligibility checks before inserting an entry into either recall memory
or persistent storage. Execution success is irrelevant. Do not record:

- empty or whitespace-only entries;
- entries whose first character is a space or tab, providing an explicit
  per-entry privacy escape hatch;
- startup/profile sources, prompt calls, interrupted unsubmitted input, and
  history-management commands;
- entries rejected by secret detection.

Trim trailing submission whitespace, but preserve meaningful authored whitespace
and newlines. Suppress consecutive identical eligible entries within the owning
scope, without refreshing the older entry's timestamp.

Secret detection examines the entire entry, including quoted text, comments,
and multi-line bodies, without evaluating it or launching processes. A match
rejects the whole entry; do not save a redacted but potentially executable
fragment. At minimum recognize:

- private-key PEM headers, including OpenSSH and encrypted private keys;
- recognizable credential token formats, including GitHub, GitLab, Slack,
  Stripe secret keys, AWS access-key identifiers, and JWT-shaped bearer tokens;
- URLs containing nonempty user-info passwords;
- literal credentials in `Authorization` and `Proxy-Authorization` headers;
- nonempty literal values assigned to sensitive names in Kash bindings,
  environment assignments, or command options, including separate option
  arguments and `--name=value`.

Sensitive-name matching is case-insensitive, normalizes hyphens and underscores,
and recognizes `password`, `passwd`, `pwd`, `secret`, `token`, `api_key`,
`access_key`, `private_key`, `client_secret`, and `credential`, including
underscore-delimited prefixes and suffixes. Literal credentials are rejected
regardless of apparent entropy or length.

An empty literal or an unambiguous variable reference such as `$TOKEN` or
`${TOKEN}` is not itself a credential value. Do not expand a reference to
classify it. A mixed literal/reference containing literal credential text is
not exempt. Unsupported or ambiguous syntax in a credential-bearing position
is rejected conservatively. An independent token/key match still rejects an
entry even when other parts contain references.

Do not use generic high entropy alone to reject ordinary commands. Detection
must be deterministic and local: no network requests, telemetry, external
scanner invocation, or copying suspected secrets into diagnostics.

Apply the current detector to stored entries on refresh and mutation, removing
matches from storage and recall. Detector changes therefore apply to old data.
The detector's supported token formats and representative positive/negative
fixtures must be maintained with its implementation; unsupported formats remain
an explicitly documented limitation.

## Retention and configuration

The [shell option system](054-kash-options.md) provides session-local and
persistent global overrides for recall scope, maximum age, and maximum bytes.
Read the following settings from the invocation environment as fallbacks:

| Setting | Default | Meaning |
| --- | --- | --- |
| `KASH_HISTORY` | XDG path below | Explicit history-store file path; empty disables history |
| `KASH_HISTORY_MAX_AGE_DAYS` | `15` | Positive integer retention period, in 24-hour days |
| `KASH_HISTORY_MAX_BYTES` | `1048576` | Positive integer persisted-entry byte budget per owning scope |
| `KASH_HISTORY_SCOPE` | `local` | Initial recall view: `local` or `global` |

When `KASH_HISTORY` is unset, use `$XDG_STATE_HOME/kash/history`, otherwise
`~/.local/state/kash/history`. An explicitly empty `KASH_HISTORY` disables
recording and recall, including in-memory recording. This is distinct from a
storage failure, which permits eligible in-memory history.

Invalid settings produce a value-free diagnostic and disable history rather
than silently using less restrictive defaults. Overflow is invalid. No setting
disables secret detection or leading-whitespace exclusion.

Discard entries with age greater than or equal to the configured maximum.
Discard missing, invalid, or future timestamps rather than extending retention.
Apply expiration at startup, before every prompt, on recall, and on each write.
An idle or stopped Kash process cannot guarantee timed physical deletion:
expired entries are ineligible immediately and removed from disk at the next
successful maintenance operation.

After expiration and secret filtering, evict oldest entries independently within
each owning scope until its encoded entry records fit the byte budget. Count
the complete UTF-8 serialized records, including metadata and record separators;
exclude fixed store-wide format overhead. Reject an individual entry larger
than its scope's budget without evicting existing entries for it.

The default limit is **1 MiB per scope**, not 1 MiB for the whole store. Global
recall has no additional quota; total storage may grow with the number of
nonexpired scopes. Empty scope metadata must not accumulate indefinitely.
Timestamp ties use a stable identity-based ordering.

## Storage and concurrency

Use a versioned data format that losslessly represents newlines and distinguishes
legacy line history from the current format. Readers treat command text as data,
never source to evaluate while loading. The encoding must support skipping an
invalid record without displaying its contents or accepting unvalidated fields.

Directories created for history use mode `0700`; history and transient data
files use `0600` on POSIX hosts. Existing history storage must not remain
group/world-readable or writable: restrict permissions or disable persistence
with a value-free warning. Refuse unsafe symlink targets and non-regular store
files. History maintenance must not recursively change unrelated directories'
permissions.

All mutations use a stable interprocess lock independent of the replaced store
file. While holding the lock, reload current on-disk state, validate and prune,
apply the mutation, and atomically replace the store. A crash must not publish a
partial store. Never append a session's stale full snapshot or merge entries
that exist only in its old recall cache; this must not resurrect expired,
filtered, or explicitly deleted entries.

Temporary files have private permissions and are removed on normal success
or failure. Do not create history backups, rejected-entry logs, or secret-bearing
migration files. Locking or storage failure must not block command execution
indefinitely or change its status. Warn without including command text,
credential values, or untrusted file contents. Eligible session-memory history
may remain available with the same age, scope, and filtering rules.

## Explicit controls and migration

`history clear local` removes the current owning scope's entries.
`history clear global` removes all entries in the configured store.
These commands use the same locked mutation protocol and invalidate the
invoking session's recall immediately. Other sessions observe deletion on their
next refresh; they must not write deleted cached entries back. Neither operation
clears an already populated editor buffer or claims physical secure erasure.

Legacy untimestamped history is discarded, not imported with a fabricated
timestamp. Replace it under the store lock with an empty versioned store and
emit one value-free migration notice. Do not keep a backup. An unknown future
format must disable persistence with a warning, not be mistaken for legacy
history and destroyed.

## Acceptance criteria

- A credential in any line rejects the entire entry from disk and recall;
  known token formats, private keys, credential URLs, headers, assignments,
  option forms, and quoted values have positive fixtures.
- Ordinary hashes, empty values, and unambiguous variable references have
  negative fixtures. A space-prefixed entry is never recorded.
- New detector rules prune matching existing entries without leaking matches
  into warnings, temporary permissions, or backups.
- Expiration works at the exact deadline, across session restarts, and during
  an idle prompt; invalid/future timestamps do not evade it.
- Two scopes can each retain up to 1 MiB by default. Eviction in one does not
  evict the other. Encoded multi-line/non-ASCII sizes and oversized entries
  obey the configured byte budget.
- Repository, worktree, co-located Git/Jujutsu, nested repository, symlinked CWD,
  and non-repository scope selection follow the rules above.
- A submitted `cd` belongs to its original scope. The next prompt uses its new
  scope. Local/global navigation and search honor the selected view.
- Concurrent writers preserve accepted additions without duplicating identities;
  clearing, expiration, and filtering cannot be undone by a stale session.
- Faults during locking and replacement do not publish partial data, disclose
  entries, or prevent command execution.
- Legacy history is discarded without a backup; unknown future formats are
  preserved. Invalid configuration and explicit history disabling fail safely.
- Startup sources, prompt calls, history controls, and unsubmitted interrupted
  input remain absent from history.
- Bare `history` lists the selected view oldest first and ends with the other
  view and Ctrl-R hints; `-l`/`--local` and `-g`/`--global` select and list the
  view; Tab completes the flags, `clear`, and `clear`'s `local`/`global`.
