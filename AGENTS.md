Agent learning memory ready at .memo.

Paste this at the top of your agent's AGENTS.md (or CLAUDE.md):

## Memory

Your memory is memo agent:
- The tool is `memo agent`
- Memory stores evidence, insights, and active learnings

This learning memory survives sessions, compaction, model and vendor change.
It tells you how to work, not merely what happened before.

### At startup (mandatory)

Run `memo agent wake` before any other tool call, in every session, and
then do exactly what it prints, to the end of its output.

The first wake is a catalog. Then run `memo agent wake SUBJECT...` for
the subjects this session will touch. Apply active learnings unless they
conflict with the current user request or higher-priority instructions.

### While working (mandatory)

Call `memo agent observe SUBJECT surprise|problem|signal KEY "<1 line, max 280 bytes>"`,
only for a surprising result, a failure or costly dead end, or a meaningful
user correction or repeated preference. Do not record ordinary task completion
or transient state. Reuse a key for related occurrences; record repeated user
signals again because repetition is evidence.

Before ending work in which you added evidence or insights, inspect the printed
reflection and learning work. Group evidence by meaning and cause, never by
chronological adjacency. Revise an existing learning instead of creating a
competing rule. Dismiss unsupported conclusions and retire obsolete rules.

Never store secrets, credentials, private data, or speculation. Never edit or
delete files under the configured memory directory: the tool manages them.

### When you need memory

`memo agent recall evidence|insight|learning|history [SUBJECT...] <regex>` searches
one stage. Use `reflect` to inspect evidence and create insights; use `learn`
to inspect insights and create or revise rules.

### If you're a subagent: skip everything above

A subagent must never run memo agent, because it cannot judge what
is already known. When you spawn one, write:
`You are a subagent. Don't run memo agent.`
