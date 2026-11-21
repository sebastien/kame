---
revision: 2
---

# Kash UI

## Purpose

Kash asks a person for decisions through **interactions**: requests to pick
items, choose between a few buttons, confirm, or enter text. Kash owns the
request, validates the answer and decides what happens next. A **backend** owns
presentation, focus and input. The built-in terminal backend, an adapter for a
client-native interface, and an embedding host answer the same requests, so
Kash code does not depend on which one answers.

This specification refines the [standard library](007-library.md),
[interactive Kash](052-kash-interactive.md), [WASM host](010-wasm.md),
[embedding APIs](028-embedding-apis.md), and [diagnostics](011-diagnostics.md).

Principles:

1. **Interactions, not widgets.** A request states what to ask. Layout, focus,
   keys and style belong to the backend. There is no widget tree, no layout
   language and no callback into Kash while a request is open.
2. **Content is presented or declined.** A backend may ignore a hint. Every
   other part of a request is presented, or the backend declines before showing
   anything and Kash offers the request to the next backend.
3. **No invented answers.** Dismissal is not the same as having no backend.
   Interactions never read answers from invocation stdin or accept a default
   on the person's behalf.
4. **Values stay in Kash.** Backends receive display text and positions, not
   the original offered values. Kash maps selection replies back to them.

## Operations

| Operation | Asks for | Answer | Cancelled |
| --- | --- | --- | --- |
| `pick! OPTIONS ITEMS` | One item, or with `multiple` any number, from a list or grid the person can filter | The item; with `multiple`, a list of the selected items in `ITEMS` order | `nil` |
| `choose! OPTIONS CHOICES` | One of one to nine choices shown together; buttons are preferred | The choice | `nil` |
| `confirm! OPTIONS` | Yes or no | `true` or `false` | `false` |
| `ask! OPTIONS` | A line of text, or with `multiline` several | The text, possibly empty | `nil` |

An interaction's kind is its operation name without `!`. The data comes last,
so `branches |> pick! "Switch branch"` works. `OPTIONS` is a string, short for
`{title: STRING}`, or a record. **Content** fields are always presented;
**hints** may be ignored.

| Field | Kinds | Role | Meaning |
| --- | --- | --- | --- |
| `title` | all | content | The question. A required nonempty string. |
| `message` | all | content | An explanation shown with the question. May span lines. |
| `label` | pick, choose | content | A callable giving each value's display string. Defaults to `str`. |
| `multiple` | pick | content | `true` lets the person select any number of items, including none. |
| `multiline` | ask | content | `true` allows line breaks in the answer. |
| `secret` | ask | content | `true` hides the text while it is typed. The interaction layer never shows, records or reports it. |
| `default` | all | hint | Initial selection, focus or input: an item, a list of items for `multiple`, a choice, a Boolean for `confirm`, or a string for `ask`. Not allowed with `secret`. |
| `detail` | pick | hint | Secondary text for each item, such as a path or a time. |
| `group` | pick | hint | A heading for each item. Consecutive items with the same heading form a group. |
| `icon` | pick | hint | `:file`, `:directory`, `:executable`, `:link`, `:command` or `:history`. |
| `layout` | pick | hint | `:list`, the default, or `:grid`. |
| `query` | pick | hint | The initial filter text. |
| `placeholder` | pick, ask | hint | Text shown while the filter or the entry is empty. |
| `accept` | pick, confirm, ask | hint | The label of the accepting action, such as `"Delete"`. |

`detail`, `group` and `icon` are a callable applied to each item, or one value
for every item; `nil` means none. Callbacks run in item order, under the
caller's authority, before anything is presented; a failing callback fails the
operation. Titles, messages, labels and details are plain text. Resolve each
item or choice default against the first still-unmatched value equal under
`==`; a missing match is invalid. A default is never accepted without the
person.

The following fail with `EXPR_INVALID` before any backend is asked: an unknown
field, or a field of another kind; a value of the wrong type, or an unknown
icon or layout; an empty title; `ITEMS` or `CHOICES` that is not a list; more
than 65,536 items; fewer than one or more than nine choices; a label that is
not a string, including items that `str` rejects without a `label`; and a
request whose encoding exceeds 1 MiB. Single-item pickers and choices cannot
offer `nil`, which is reserved for dismissal. A negative confirmation and a
dismissal are distinct in the backend reply but both return `false` to Kash.

```kash
let branch = $(git branch --format='%(refname:short)') |> line |> split "\n" |> pick! "Switch branch"
if branch
	git switch $branch

if confirm! {title: "Show the diff?", message: $(git status --short)}
	git diff

let existing = choose! "config.toml exists" [:overwrite, :keep, :abort]
let token = ask! {title: "API token", secret: true}
```

### Phases and ordering

Interactions are execution-only volatile observations. They are valid in Kash
scripts, interactive entries, explicit expression execution, and `task` bodies
that are not `cached`. They are not permitted in planning, dry-run, template
rendering, prompt hooks, deferred cleanup or finalizers. Disallowed calls fail
with `PHASE_INVALID` before dispatch. They need no capability grant; whether
a backend exists is host configuration.
Answers register no dependency and are never cached. A suspended computation
that resumes does not ask again for an answer it already received.

An invocation presents one interaction at a time. Further requests, from
concurrent tasks or `&` work, wait in request order. In an interactive session,
a request from a background job stops that job as a terminal read would
(`stopped (tty input)`, `052`), whatever the backend; `fg` presents it.

## Backends

### Selection

The native CLI reads `KASH_UI` from the invocation environment. It lists
backends, separated by the host's path-list separator (`:` on POSIX), in the
order they are tried. `terminal` is the built-in terminal backend. An absolute
path names an adapter. Unset means `terminal`, and empty means no backend. A
value with any other entry, including an empty one, is invalid: every
interaction then fails with `UI_UNAVAILABLE` naming the position of the first
invalid entry, and no backend is guessed. The setting is fixed for the
invocation; `set env/KASH_UI` affects only processes started later.

Kash does not automatically select a client-native backend. On all hosts,
editor history and `ask!` with `secret: true` go only to the built-in terminal
backend, if enabled, and fail with `UI_UNAVAILABLE` otherwise. The evaluator
must not forward secret requests to an embedding callback. Other requests may
expose their display content to the configured backend; a native client may
display it on another device.

Kash offers a request to each backend in turn. A backend either **presents**
it, after which its answer, cancellation or failure is final, or **declines**
without showing anything, and Kash tries the next. When every backend declines,
the interaction fails with `UI_UNAVAILABLE`. Its cause lists each backend with
its reason:

| Reason | Meaning |
| --- | --- |
| `unsupported` | The backend cannot present this request: its protocol, its kind, a content field, or its size. |
| `unavailable` | The backend cannot present anything now, for example without a terminal or a client. |
| `busy` | Something other than Kash holds the backend, such as a process given the terminal. |

### Protocol

A request and its answer are each one UTF-8 JSON object, the same for
adapters, WASM hosts and JavaScript embeddings:

```json
{"protocol": 1, "kind": "pick", "title": "Delete branches", "multiple": true,
 "items": [{"label": "main", "hints": {"detail": "origin/main"}}, {"label": "fix-ui"}],
 "hints": {"default": [1], "accept": "Delete"}}
```

| Field | Contents |
| --- | --- |
| `protocol` | `1`. |
| `kind` | `"pick"`, `"choose"`, `"confirm"` or `"ask"`. |
| `title`, `message` | Strings. `message` is omitted when absent. |
| `items` | For pick, one `{"label": STRING}` per item, with a `hints` object holding its `detail`, `group` and `icon` when they are set. |
| `choices` | For choose, one `{"label": STRING}` per choice. |
| `multiple`, `multiline`, `secret` | `true` when set, otherwise omitted. |
| `hints` | The request's hints: `default` as an index, an array of indexes, a Boolean or a string, by kind; `layout`, `query`, `placeholder` and `accept` as strings. Omitted when empty. |

Symbols are encoded as their names. Items and choices are identified by their
zero-based positions.

A backend declines as `unsupported` a request whose protocol, kind, or any
field outside a `hints` object it does not implement. Inside `hints` objects it
ignores what it does not recognize. Later protocol revisions add meaning only
outside `hints` and presentation only inside, so an older backend declines what
it cannot honor and presents everything else.

An answer is exactly one of:

| Answer | Meaning |
| --- | --- |
| `{"answer": VALUE}` | For pick, an index, or with `multiple` an array of distinct indexes; for choose, an index; for confirm, a Boolean; for ask, a string. |
| `{"cancelled": true}` | The person dismissed the interaction. |
| `{"declined": REASON}` | One of the reasons above. |

Kash checks each answer against its request: index ranges and distinctness,
types, UTF-8 text without NUL, and line breaks only with `multiline`. The
encoded reply is limited to 1 MiB. A malformed, oversized or mismatched
answer fails the interaction with
`UI_PROTOCOL`. The request is not offered to another backend, because the
person may already have seen it.

### Adapters

An adapter presents interactions outside the terminal backend, for example in
the native command palette of a terminal client, or with a terminal interface
of its own. For each interaction, Kash:

1. Starts the executable directly, without arguments or a shell, in the session
   directory with the session child environment, and with `KASH_UI` set to the
   entries after it.
2. Writes the request and a newline to its stdin, then closes stdin.
3. Reads exactly one answer line from its stdout and requires exit status 0.
   A missing answer, extra output or another status is `UI_PROTOCOL`.
4. Drains and discards its stderr.

An adapter that cannot be started declines as `unavailable`. An adapter
declines without waiting for a person. In an interactive session its process
group receives the terminal foreground under the handoff of `052`, while its
standard streams remain the protocol pipes; elsewhere it shares the
invocation's terminal like any child process. Either way it may draw on the
terminal, and Ctrl-C and Ctrl-Z reach it: an adapter that dies of `SIGINT`
interrupts the request's entry or invocation (status 130, no diagnostic), and
one that stops stops its job. Cancelling the interaction terminates and reaps
the adapter's process group, and a late answer is ignored. Adapters are trusted
configuration running with the user's authority, like plugins (`033`). Kash
does not discover, install or sandbox them.

For example, an adapter for a terminal client with a native palette declines as
`unavailable` outside that client. Inside it, it translates `pick` and `ask`
requests to the palette, and declines as `unsupported` what the palette cannot
show, such as `message`, `secret` or too many items. With
`KASH_UI=/path/to/adapter:terminal`, one configuration uses the palette inside
that client and the terminal elsewhere. Rex can show the prompt on every
eligible connected client. Its `no_client` can also mean that a displayed
prompt lost its client: the adapter must not decline if it cannot establish
that the request was never presented.

### Embedding hosts

WASM hosts receive an interaction as request kind 47. Its payload is the request
object and its completion is the answer object, under the ordinary completion,
cancellation and generation rules. JavaScript embeddings service it through
`hostRequest` with a `json` completion. The default Node host completes it with
`{"declined": "unavailable"}`.

### Terminal backend

The terminal backend presents interactions on the controlling terminal device,
not on the standard streams, so it works while stdin or stdout is a pipe, a file
or a capture. It runs on POSIX hosts and declines as `unsupported` elsewhere. It
declines as `unavailable` without a controlling terminal and as `busy` while the
terminal is lent to a process graph.

- While open, it holds the terminal in raw mode. On every exit path, including
  cancellation, handled signals and errors, it erases what it drew and restores the
  terminal modes. Build presentation does not draw meanwhile.
- Input already pending when it opens is never delivered to it, so typed-ahead
  text cannot answer a question. An interactive session keeps that input for
  later entries; other invocations discard it.
- Esc cancels. Ctrl-C interrupts the request's entry or invocation as `SIGINT`
  would. Ctrl-Z erases the interaction and stops the request's job, or outside
  an interactive session the process, as `SIGTSTP` would; resuming presents the
  request again from its initial state.

It draws inline below the cursor and follows resizes. The title comes first,
then the message, the control and a key hint. Content that does not fit
remains reachable by scrolling. If the terminal is too small for rich
presentation, use line mode. Layout uses
display width: wide characters take two cells, and text is truncated at
grapheme boundaries with `…`. Control characters, including ESC, are shown
escaped and never sent to the terminal. Focus is marked with `>` and reverse
video, and selection with `[x]` and `[ ]`; color only adds emphasis and is off
under `NO_COLOR`.

| Kind | Keys |
| --- | --- |
| pick | Typing, Backspace and Ctrl-U edit the filter. Up and Down (Ctrl-P and Ctrl-N), Page Up and Page Down, and in a grid Left and Right, move the focus. With `multiple`, Tab toggles the focused item and Enter accepts the selection, including an empty list. Otherwise Enter accepts the focused item, or does nothing if there is none. |
| choose | Left, Right and Tab move the focus. 1 to 9 accept that choice. Enter accepts the focused choice. |
| confirm | `y` answers yes and `n` no. Left, Right and Tab move the focus. Enter accepts the focused button, which is no unless `default` is `true`. |
| ask | Left, Right, Home, End, Backspace, Delete, Ctrl-A, Ctrl-E, Ctrl-U, Ctrl-K and Ctrl-W edit. A bracketed paste is inserted without accepting, with its line breaks dropped unless `multiline`. Enter accepts; with `multiline` it inserts a line break and Ctrl-D accepts. With `secret` nothing is shown, not even the length. |

The pick filter keeps the items whose label contains every blank-separated
filter term, ignoring case. Kept items stay in order under their group headings,
and empty groups are hidden. Selected items stay selected while hidden. A grid
fills rows left to right with equal-width columns, as many as fit, and starts a
new row for each group.

**Line mode.** When `TERM` is unset, empty or `dumb`, the backend writes plain
lines without cursor movement or color. It writes the title and the message,
then:

- pick and choose list their items numbered from 1, pick listing at most 100
  kept items and a count of the rest. A number answers, or with `multiple`
  blank-separated numbers; an empty line accepts an empty multiple selection.
  Other text replaces the pick filter and lists again.
- confirm reads `y`, `yes`, `n` or `no` in any case; an empty line answers the
  default, which is no unless `default` is `true`.
- ask reads one line, with echo disabled for `secret`, or with `multiline` the
  lines up to one containing only `.`.

An invalid reply asks again, and end of input cancels.

### Program status

The terminal backend may report that it is waiting using
[OSC 7501](https://www.superlogical.com/rex/docs/build/program-status). The
record uses `state=blocked` and carries no request text or answer. The backend
clears only its own record when it stops waiting. This is status, not a prompt
transport. Line mode and redirected streams never carry these reports.

## Editor pickers

On a terminal session the line editor presents three pickers through the same
backends. They are editor commands, bindable in inputrc under `$if kash`:

| Command | Default key | Items | Accepting |
| --- | --- | --- | --- |
| `kash-pick-history` | Ctrl-X r | Entries of the selected recall view (`053`), most recent first. The label is the first line, followed by `…` when there are more, and the detail is the submission time, with the directory in the global view. The edited entry is the initial query. | Replaces the edited entry with the whole chosen entry. |
| `kash-pick-file` | Ctrl-X f | Paths under the session directory, breadth-first and at most 65,536, with `multiple`. Names starting with `.`, and their contents, appear only when the word before the cursor starts with `.`. Symlinked directories are listed but not entered. That word is the initial query. | Replaces that word with the chosen paths, escaped as completion inserts them and separated by spaces. |
| `kash-pick-command` | Ctrl-X c | The command-position completion candidates of `052`, under their headings, as a grid. The word before the cursor is the initial query. | Replaces that word with the chosen name. |

- Accepting changes the edited entry and never runs it. Undo reverts the change
  in one step.
- Esc and Ctrl-C, including an adapter ending on `SIGINT`, close the picker and
  leave the entry and the cursor unchanged. Ctrl-Z has no effect, as at the
  prompt. When no backend presents the picker, the editor shows the reasons as
  a hint and leaves the entry unchanged.
- History items follow the eligibility and expiry rules of `053` at
  presentation and acceptance. File and command pickers never send
  unsubmitted editor text to an adapter, including as a query.
- Editor pickers use bounded snapshots within the protocol limits and
  indicate if more results exist than fit. An empty selection leaves the
  editor unchanged.
- Tab completion and Ctrl-R incremental search remain part of the editor and do
  not use backends. Binding Ctrl-R to `kash-pick-history` replaces the search
  with the picker.

## Diagnostics

| Code | Severity | Meaning |
| --- | --- | --- |
| `UI_UNAVAILABLE` | error | No backend presented an interaction. The cause lists each backend with its reason, or the invalid `KASH_UI` entry. |
| `UI_PROTOCOL` | error | A backend's answer, output or exit status violated the protocol. |

Neither includes request content or answers. Both fail the requesting
statement like any other failure, and `??` does not recover them.

## Non-goals

- Forms with several controls, layout trees, custom widgets, styled labels and
  mouse input.
- Streamed or live-updating items, previews, and timeouts other than the
  invocation's own.
- Interactions as pipeline stages (`… | pick!`). Capture and split instead.
- Backends for the line editor itself, its completion menu or its incremental
  search.
- Adapter discovery, persistent adapter processes, and negotiation beyond
  declining.
- Status or notification lines outside an open interaction.
- Changes to `kame do pick`, which keeps its external pickers (`009`).

## Conformance

| Contract | Required executable checks |
| --- | --- |
| Operations | Title shorthand and option records; data-last pipes; default and callback labels, including paths and records; answers mapped to the offered values, with `multiple` in `ITEMS` order; cancellation results, including `false` from `confirm!`; every `EXPR_INVALID` case rejected before a backend starts; `PHASE_INVALID` in declarations, rule bodies and cached tasks. |
| Dispatch | List order; fallthrough on each decline reason, including an adapter that cannot start; unset, empty and invalid `KASH_UI`; `UI_UNAVAILABLE` causes without request content; concurrent requests presented one at a time in order; a background job stopped by its request and presented by `fg`; `??` recovering neither code. |
| Protocol | The encoding of every kind and field; unknown fields declined outside `hints` and ignored inside; out-of-range, duplicate, mistyped, oversized, NUL-bearing and multi-line answers, unknown answer fields and malformed JSON failing with `UI_PROTOCOL` without another backend being asked. |
| Adapters | Direct execution without arguments, in the session directory and environment, with the rest of the list as `KASH_UI`; one request line in and one answer line out; nonzero status and extra output rejected; stderr discarded; process groups reaped on cancellation and interrupt; under a pseudo-terminal, Ctrl-C reporting 130 without a diagnostic and Ctrl-Z stopping the job. |
| Terminal backend | Under a pseudo-terminal: redirected stdout and captures; declines without a terminal and while a process holds it; mode restoration; typed-ahead input never answering; escaped control characters; wide characters and truncation; keys, grids, groups and multiple selection; Ctrl-Z and resume; `TERM=dumb`; `NO_COLOR`; if emitted, OSC 7501 report and clear without payload text. |
| Editor pickers | Under a pseudo-terminal: each command; history scope, eligibility and insertion without execution; escaped paths, hidden names, the symlink rule and snapshot bounds; cancellation and one-step undo. Adapters get neither history nor unsubmitted editor text. |
| Embedding | Kind 47 round trips through a `hostRequest` callback; the default Node host declining; cancellation and late completions. |
