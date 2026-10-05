# Runtime performance and allocation contract

## Scope

D08 is limited to the measured parser and runtime costs listed here. Existing
owning APIs keep their ownership guarantees; faster APIs state when the caller
must retain storage. Optimizations preserve authored byte spans, lexical
shadowing, graph identity, glob ordering, diagnostics, and native/WASM results.

## Source ownership and parsing

`source.New` and `script.Parse` own copies of their inputs. Borrowing APIs may
retain the caller's name and text without copying. The caller keeps both strings
alive and immutable until the parsed script is freed. Parsing remains byte
oriented and does not decode or normalize source text; UTF-8 decoding is needed
only when mapping a byte span to display columns.

Acceptance compares the tracker allocation delta of owning and borrowing parse
paths for the same representative source. The borrowing path must allocate at
least the source-text byte length less while producing equivalent AST kinds,
text, diagnostics, and spans. Existing owning APIs must continue to work after
the caller releases its input.

## Lookup and traversal

Engine resource lookup and lexical scope lookup use indexes for repeated key
queries. Indexes confirm complete keys after hash matches, preserve last-binding
shadowing, and remain consistent when nodes or bindings are added and released.

Glob expansion visits only directories compatible with the literal and
wildcard path segments. `**` retains recursive semantics, results remain
sorted and duplicate-free, and watch invalidation continues to use the complete
pattern membership. Acceptance records directory entries visited for a large
fixture with unrelated sibling trees and requires fewer visits than a full
walk, while returning byte-identical results.

## Pure evaluation

Synchronous host-free expressions complete without entering an engine wait or
poll loop. Expressions that need a host request return the existing
`HostNeeded`/request contract; capability failures and diagnostics remain
unchanged. Tests cover literals, local definitions, pure operations, lazy
host-backed values, and denied effects.

## Measurements

The acceptance suite records tracker total allocations for ownership-sensitive
paths and deterministic workload elapsed times for source parsing, deep and
wide scope lookup, graph lookup over many resource keys, and wildcard traversal
with irrelevant directory trees. Memory and traversal-count assertions are
deterministic gates. Wall-clock measurements are reported as repeated medians
and are informational, not brittle pass/fail thresholds. Native and
portable-runtime results are recorded separately.

`D08` remains open until each section above has implementation and focused
native/WASM or portable-runtime evidence, and the complete measurement workload
has been rerun after the changes.

Current evidence includes `TestBorrowedParseMatchesOwningParseWithFewerAllocations`,
`TestEngineResourceIndexKeepsNodeIdentity`, `TestLexicalFunctionAndOperationShadowing`,
`TestPureExpressionUsesPortableEvaluator`, and
`TestWildcardTraversalPrunesUnrelatedSubtrees`. The repeatable deep-tree gate
counts enumerated entries against a full recursive walk. `tools/benchmark-cli.py`
reports five-sample native/WASM measurements for all four runtime workloads and
large-source parsing; broad scope/graph tracker-allocation totals remain
outstanding. See `docs/review-performance.md` for the 2026-10-06 run and limits.
