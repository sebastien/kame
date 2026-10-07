# Job and CLI presentation contract

A job is a requested target; a
process is one host execution performed while a job is active. Presentation is
observational and must not alter scheduling, cancellation, or process policy.

## Human progress

Human mode reports target start, completion, failure, and cancellation on
stderr. Cancellation has its own wording and count, never appears as failure,
and keeps the invocation's nonzero cancellation status. Summary counts separate
completed, failed, and cancelled targets. Concurrent jobs retain target names
so interleaved lines remain attributable.

For each process, human mode displays its target, executable, bounded argv, and
elapsed milliseconds. It omits environment values and process input. At most
eight arguments and 160 UTF-8 bytes are shown; truncation is explicit. Pipelines
show at most four stages with the same per-stage bounds. Process streams remain
on their existing output streams.

## JSON events

JSON mode writes only schema-versioned event objects to stdout and leaves stderr
empty. It carries the same target and process lifecycle, including cancellation
and process runtime, without human progress lines or ANSI styling. Existing
event field names and meanings remain stable; new process display fields are
optional and bounded. Environment values are never included.

## Color and lessons

`--color auto|always|never` controls human diagnostics and progress consistently
with 011, including `NO_COLOR`, `CLICOLOR_FORCE`, and non-terminal output. JSON
and plain diagnostic output contain no ANSI escapes. Help and documentation
examples that advertise executable commands are exercised by acceptance tests
from a clean temporary project.

## Acceptance

Tests cover concurrent target identity, separate cancellation reporting and
summary counts, bounded direct-argv and pipeline display, elapsed runtime, JSON
stream separation and schema stability, color environment behavior, and the
documented lessons. Native and WASM CLI output is compared where both expose the
same host event data; host-specific timing values are checked for type and
non-negativity rather than exact equality.
