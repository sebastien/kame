## Memory (delegated to cheap subagent)

Your memory is memo agent, run ONLY through a dedicated `memo` subagent:
- The tool is `memo agent`
- Run it via the `subagent` tool with `agent: memo`, `model: openai/gpt-6-luna`
- Memory stores evidence, insights, and active learnings

This learning memory survives sessions, compaction, model and vendor change.
It tells you how to work, not merely what happened before.

### At startup (mandatory)

Delegate `memo agent wake` through the `memo` subagent before any other tool call, in every session, and
then do exactly what it returns, to the end of its output.

A wake with no subjects loads all configured, available memory. Delegate
`memo agent wake SUBJECT...` to focus on selected subjects. Apply active learnings unless they
conflict with the current user request or higher-priority instructions.

### While working (mandatory)

Delegate `memo agent observe SUBJECT surprise|problem|signal KEY "<1 line, max 280 bytes>"` through the `memo` subagent,
only for a surprising result, a failure or costly dead end, or a meaningful
user correction or repeated preference. Do not record ordinary task completion
or transient state. Reuse a key for related occurrences; record repeated user
signals again because repetition is evidence.

Before ending work in which you added evidence or insights, delegate the printed
reflection and learning work through the `memo` subagent. Group evidence by meaning and cause, never by
chronological adjacency. Revise an existing learning instead of creating a
competing rule. Dismiss unsupported conclusions and retire obsolete rules.

Never store secrets, credentials, private data, or speculation. Never edit or
delete files under the configured memory directory: the tool manages them.
Never run `memo agent` yourself directly; always delegate to the `memo` subagent.

### When you need memory

Delegate `memo agent recall evidence|insight|learning|history [SUBJECT...] <regex>` through the `memo` subagent; it searches
one stage. Use `reflect` to inspect evidence and create insights; use `learn`
to inspect insights and create or revise rules.

### If you're a subagent other than memo: skip everything above

A subagent other than `memo` must never run memo agent, because it cannot judge what
is already known. The `memo` subagent is the sole exception: it runs the requested
`memo agent` commands and returns raw output. When you spawn it, write:
`You are the memo subagent. Run the requested memo agent command.`
