---
created: 2026-09-27T22:53:25+00:00
updated: 2026-11-22T21:03:37.347886+00:00
uuid: 93d8d59c-bf85-4001-b8d0-a830ee0b20fa
identity: c2c1d894-929e-4bf0-9f83-7a73db1431cb
---
# Early Kame architecture brief

Status: superseded. The implementation target has changed; see
`docs/spec/001-architecture.md` for current architecture. The original brief
is preserved in Git at `4289d6b094b69b4ee2f54b7da28f303972e439d5`.

Using `../littlemake` as reference, and start to form a view of the domain, architecture
and structure of littlemake.

- Read and sum up `../littlemake/docs/spec/*` to get a view of the different areas
- Read and sum up `../littlemake/src/ts/*` to get a view of the current implementation

Here is my personal take, as the primary author:

- Littlemake, at its core, is a reactive streaming engine, where computation nodes
  can produce one value (either at once, or incrementally), or a stream of values,
  leading then typially to multiple versions/results.
- Computation nodes are mapped to a VFS-like tree, either in the form of a named
  rule (to trigger production of dependencies) or as the output artifact itself
  (such as the output file).
- Computation graphs are mostly dynamic, although they typically quickly converge
  to a static state (eg. discovering files/dependencies, and then evaluating).
- That incremental, streaming reactive core is augmented by a standard library
  to access filesystem, process filenames, and generally run and monitor processses.
  This is key as littlemake is primarily an orchestration tool.
- Finally, the littlemake languages (template, expression, rules) should be
  embeddable and composed, so that you can parse/eval/fmt each of them
  independently.

The way I see the architecture is like so:
- core: the incremental, lazy, streaming reactive engine and its primitives (cells, etc)
- tools: common operations on the core (typically exposed through the CLI/API)
- lib: the standard library of functions, designed for async streams
- lang: the DSLs for template, expression and rules/program.
- cli: the CLI interface
- utils: anything that should be reusable

Some recommendations:
- Performance relies heavily on automatic parallelisation and caching,
- Process and process group management is absolutely key, in particular lifecycle,
  cleanup, signal management.
- We would still love LittleMake's reactive core to be an embeddable engine thanks
  to the WASM target. If it can be used client side or server side with JavaSript
  all the better.

Implementation:
- Clean, minimal
