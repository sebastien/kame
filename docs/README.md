# Kame documentation

Use these references and examples with a CLI built from matching sources.
For older syntax, follow [the migration note](../MIGRATION.md).

## Start here

- [Quickstart and build example](../README.md#get-started)
- [Installation and backends](ref-installation.md): release verification, host requirements, launcher setup
- [Runnable lessons](../examples/README.md): independent values, rules and Kash processes, then composition
- [Migrating from GNU Make](skills/kame/ref/make-migration.md)
- [Idioms and gotchas](idioms-and-gotchas.md)
- [Glossary](glossary.md)

## Language and CLI references

- [Language overview](skills/kame/ref/language.md)
- [Expressions](skills/kame/ref/expressions.md) and [standard library](skills/kame/ref/library.md)
- [Scripts and includes](skills/kame/ref/scripts.md)
- [Build rules](skills/kame/ref/rules.md)
- [Templates](skills/kame/ref/templates.md)
- [Kash processes](skills/kame/ref/kash.md)
- [Kash operational cookbook](kash-cookbook.md): argv, status policy, finite retry, scoped environments and deferred cleanup
- [CLI](skills/kame/ref/cli.md)

## Integrate and contribute

- [Embedding APIs](ref-embedding.md): JavaScript/WASM and asynchronous Python/CLI usage
- [Contributing](ref-contributing.md): toolchain, repository map, local checks, documentation maintenance

## Normative specifications

The [specification index](spec/README.md) covers language, engine, host boundaries,
interfaces and acceptance requirements. Specs define intended behavior, not
implementation status; references explain usage. The applicable spec governs
when they disagree.

## Workflow scope assessments

These documents constrain SDK migration work; they are not general language
tutorials or promises of new native operations.

- [SDK migration boundaries](sdk-migration-boundaries.md): truth/emptiness, lossless capture, cleanup ownership, and vendor/workflow scope
- [XML extraction requirements scope](xml-extraction-scope.md)

## Remaining work

[TODO.md](../TODO.md) is the single backlog; [tests/CATALOG.tsv](../tests/CATALOG.tsv)
is the CLI suite inventory, not a conformance or pass report.
