# Rule precedence

## Purpose

Permit a specialized file-output namespace to override a general template
without making producer selection depend on source existence or source order.

```kame
./www/{path:**}.html : ./build/html/{path}.html
	...

./www/xml/{path:**}.html : ./build/xml/{path}.html
	...
```

Requesting `./www/xml/feed.html` selects the second rule with `path = feed`.
The first rule remains available for outputs outside that specialized namespace.

## Selection contract

1. A literal output retains precedence over every template output.
2. Otherwise collect every matching output template and its capture bindings.
3. Discard candidates dominated by a strictly more specific matching template.
4. Select the remaining rule instance if it is unique. Equal or incomparable
   remaining instances fail with `TGT_AMBIG`, identifying competing declarations.

Selection is independent of declaration order, prerequisite existence, cached
outputs, and whether prerequisites have producers. A selected rule's missing or
failed prerequisite is an error, never permission to retry a broader rule.
Planning, execution, inspection and target lookup share this selection algorithm.

## Supported specificity relation

This version defines structural specificity for file templates containing exactly
one capture, whose matcher is `*` (including `{name}`) or `**`. Capture names and
anonymous versus named spelling do not affect precedence.

Write such a template as `PREFIX CAPTURE SUFFIX`, using decoded literal parts.
Compare prefixes in absolute working-directory coordinates, preserving their
boundary with the capture. Template A strictly specializes template B when:

- A's prefix starts with B's prefix;
- A's suffix ends with B's suffix;
- B uses `**`, or A uses `*` and the extra prefix/suffix bytes contain no `/`;
- at least one literal boundary is extended, or A uses `*` where B uses `**`.

These conditions guarantee containment, not a literal-character score. For
example, `./www/xml/{p:**}.html` specializes `./www/{p:**}.html`,
`./out/{p}.html` specializes `./out/{p:**}.html`, and
`./out/{p}.min.js` specializes `./out/{p}.js`.
But `./out/xml/{p:**}` and `./out/{p:**}.html` are incomparable, even when
one has more literal bytes. A third template specializing both can resolve
their intersection; ambiguity must not be reported before all matches are known.

Multiple captures, regex captures, character classes and other glob matchers
retain existing ambiguity behavior. No general regex/glob inclusion solver,
numeric priority, declaration-order tie-breaker or source probing is introduced.
Logical-task template selection is unchanged.

## Rule instances and grouped outputs

One declaration matching through several outputs with identical capture bindings
still denotes one instance. Different capture bindings from matching outputs of
the same declaration remain `TGT_AMBIG`, even if those output templates have a
specificity relationship.

A grouped producer must own every rendered sibling: each sibling must select
the same declaration and capture bindings. Planning rejects a group with
`TGT_AMBIG` if a sibling is owned by a literal or more-specific producer, or has
ambiguous ownership. A request through an unshadowed sibling must not execute
the broader group and overwrite a specialized output. Groups are not split and
their recipes are not partially executed.

Generated concrete declarations keep their registration-time collision checks.
This change does not relax duplicate literal ownership.

## Verification

- Nested prefix and suffix specializations select the expected captures in either
  declaration order, including chains and a candidate dominating two overlaps.
- `*` specializes `**`; capture renaming and shorthand create no priority.
- Equal, incomparable and unsupported competing templates remain ambiguous.
- A literal wins over nested templates.
- A missing specialized input fails even when the broader input exists.
- Inspection and materialization agree with planning.
- Grouped-output shadowing fails before recipe execution in either request order;
  unshadowed groups continue to share one producer instance.
- Native and WASM use the same portable selection implementation.
