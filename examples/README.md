# Kame examples

**Describe values. Describe dependencies. Describe processes. Then discover
the computation they describe together.**

## Three independent entrances, one shared reveal

| Lesson | Question | Source | Status |
| --- | --- | --- | --- |
| [Expressions](expressions/README.md) | What value do I want? | `.km` | Runnable via `-f FILE NAME`; direct file execution planned |
| [Rules](rules/README.md) | What must exist, and what does it depend on? | `.kmk` | Runnable |
| [Kash](shell/README.md) | How should processes cooperate? | `.kash` | Standalone sources staged; embedded capture runnable |
| [Composition](composition/publication/README.md) | How do the descriptions fit together? | All three | Values and rules runnable; Kash entry staged |
| [Reactivity](reactive/README.md) | How does a requested computation stay current? | Persistent engine | Engine demo runnable; live publication runner future work |

Follow the rows in order for the guided path, or enter through whichever layer
you need. Each standalone lesson has its own inputs. The common project is a
tiny text publication: two notes become pages and an index, with no external
publishing dependencies.

Each lesson asks a question, supplies a small complete source, shows how to run
it and what to observe, then gives one change to experiment with. Do the change:
the lesson is in the behavior, not just the syntax.

## Getting started

Build the CLI from the repository root:

```sh
make build/kame.debug
(cd examples/expressions && ../../build/kame.debug -f ./01-values.km publication)
(cd examples/rules/publication && ../../../build/kame.debug)
```

The lessons use `kame` for readability. Substitute the absolute path to
`build/kame.debug`, or use an installed `kame` on PATH. Run lesson commands from
the stated example directory so generated files remain local.

The examples regression test uses temporary copies rather than modifying lesson
inputs:

```sh
bash tests/T013-04-meta-layer-examples.sh
```

## Component map

| Component | Start here |
| --- | --- |
| Lists, records, paths, references, lazy definitions | `expressions/01-values.km` |
| Functions, lambdas, patterns, value pipes | `expressions/02-functions.km` |
| Resource discovery and tracked inputs | `expressions/03-resources.km` |
| File artifacts, bare actions, captures, computed dependencies, recipes | `rules/publication/Makefile.kmk` |
| Typed argv, explicit expression boundaries, command capture | `shell/01-values.kash` and its runnable README commands |
| Process streams and redirection versus value transformations | `shell/02-pipelines.kash` |
| Command/value recovery and branch selection | `shell/03-recovery.kash` |
| Native source composition and explicit subprocess boundaries | `composition/publication/` |
| Demand, invalidation, subscriptions, generations and streams | `reactive/README.md`, `engine/` |

This is a teaching map, not an exhaustive syntax reference. Continue with the
[language](../docs/spec/004-language.md), [library](../docs/spec/007-library.md),
and [Kash](../docs/spec/017-kash.md) specifications for the full contracts.

## More examples

- [`language/`](language/) contains the original small runnable `.kmk` projects.
- [`tutorial-blog/`](tutorial-blog/) builds a minimalist personal blog from
  Markdown into HTML/CSS with pandoc; its README is the longer tutorial.
- [`engine/`](engine/) contains executable Go examples of the reactive engine
  API, shared by the demo and tests.
