# XML extraction scope

This is a requirements inventory, not a new XML operation or selector grammar.
CSV remains implemented by the existing SDK Kame reader/writer; no native CSV
syntax or replacement is needed. Its quoting, CRLF, multiline, empty-input,
final-row and large-input regressions must remain intact.

## Evidence and minimum extraction behavior

The SDK's legacy `sdk.mk/bin/sdk-collate` uses ElementTree `root.itertext()`;
`sdk.km/src/km/collate.km` handles JSON, and `bin/sdk-collate.kmk` explicitly rejects
XML sources. The legacy XML path has no configured XML selectors or XPath calls.
Do not confuse JSON key paths (`.`, `*`, `@`) with an established XML API.

Required collation behavior:

- Parse a well-formed XML document, retaining text nodes and child tails in
  document order. Mixed content such as `<p>before<b>inside</b>after</p>` supplies
  three text segments, not one flattened string for the whole element.
- Decode predefined/numeric XML entities and treat CDATA as text; do not extract
  comments, processing instructions, tag names, or attribute values by default.
- Keep parsing lossless with respect to text segments. The caller performs the
  legacy collation normalization: collapse Unicode whitespace within each
  segment, trim it, and omit empty segments. First-occurrence deduplication and
  CSV framing belong to collation, not the XML parser.
- Source attribution remains an explicit caller value (component or file name),
  not a parser-owned heuristic. Reads compose with ordinary tracked `read`;
  parsing must not load paths or URLs implicitly.
- `.xml.paml` is only a legacy discovery suffix: it was fed to an XML parser.
  Do not interpret PAML or malformed HTML as XML without an explicit conversion.

## Selectors and namespaces

Text extraction requires no general selector engine. A public XML tree/query API
should not be chosen merely to duplicate the JSON collator. Establish real
element/attribute selection callers before selecting any subset of XPath or CSS.
If selectors are later required, specify child versus descendant axes, wildcard
matching, attribute access, zero/multiple matches, stable document order,
duplicate handling, and unsupported-selector diagnostics explicitly.

Namespace correctness is still required at parse time: expanded names consist
of namespace URI and local name, not a source prefix. Default namespaces apply
to elements, not unprefixed attributes; prefix rebinding must be scoped.
Different prefixes for the same URI must not change a query's meaning. Reject
undeclared prefixes and malformed/duplicate expanded attributes. Any selector
namespace map must be explicit; local-name-only matching is opt-in rather than
an accidental default.

## Parser-safety gate before API selection

- Reject DTDs and user-defined entity declarations. Only predefined entities
  and numeric character references are required. Never resolve external entities,
  network resources, external subsets, schemas, or XInclude.
- Enforce bounded input bytes, nesting depth, node/text counts, attribute counts
  and decoded output sizes. Agree on concrete ceilings from real fixtures before
  selecting an API; native and WASM must enforce the same limits and fail with
  diagnostics rather than stack/arena exhaustion or partial successful values.
- Specify accepted encodings and XML declarations; UTF-8 input is the minimum.
  Reject invalid encoding, illegal XML code points, malformed structure, and
  trailing multiple roots. Do not silently recover as HTML.
- Preserve authored source positions in parse/limit failures. A caller may choose
  to warn and skip an invalid file (legacy behavior), but the parser must return
  a failure rather than an indistinguishable empty successful extraction.
- Choose an implementation only after checking portable/freestanding support,
  allocator ownership and bounded decoding. A regex or substring extractor is
  not a safe XML parser; a host-only parser is not native/WASM parity.

The eventual acceptance inventory includes nested mixed text/tails, CDATA and
entities, Unicode whitespace, namespaces/default namespaces/prefix rebinding,
comments and processing instructions, malformed XML/encoding, external-entity
and expansion attacks, and every agreed limit boundary. Existing CSV regression
coverage remains independent of that decision.
