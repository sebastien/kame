# Output ownership and variable membership

## Existing mechanisms

Grouped file targets belong to one producer instance and execute once for a
shared capture binding. All declared siblings must be present and readable after
execution. Their content signatures and ordered output-set signature participate
in reuse: missing or modified siblings invalidate reuse even when a different
sibling was requested. Downstream rules can depend on any declared sibling.

Producer reuse validates the whole group; consumer reuse observes only the
content of each artifact actually declared or read. Re-executing the producer,
or changing another sibling, does not invalidate a consumer of byte-identical
output. All siblings still share one execution, and publication waits for every
declared output to be readable. Discovered reads use the same per-artifact
boundary as explicit prerequisites.

Computed output expressions are evaluated during pure registration. They can
splice a configured list of paths into a grouped producer; they cannot discover
membership by reading a directory, manifest, or running a compiler. A changed
configured list changes the producer's output-set identity. Removed paths cease
to be declared outputs, but Kame does not delete their physical files.

A directory path is not a recursive output declaration. File publication verifies
readable artifact bytes, not a directory tree inventory. Glob prerequisites track
input membership; they do not claim ownership of matching outputs. A manifest is
an ordinary file artifact, not an output-registration protocol. Reading it as a
computed prerequisite tracks the read and names its downstream dependencies, but
does not make the manifest producer the owner of every listed file.

## Supported contract

Prefer grouped outputs when membership is known from pure configuration; prefer
one stable aggregate artifact when a tool's internal file set is not known until
execution. A manifest-plus-pattern design can enumerate downstream prerequisites
provided each member already has an unambiguous registered producer. A sequenced
prerequisite can build the manifest before discovering those input names.

Literal producers retain precedence over pattern producers. Competing matching
patterns are rejected without probing source files. Canonically identical literal
outputs are rejected at registration. Output ownership never follows a wildcard
or changes according to which source happens to exist.

## Cleanup and runtime output-set boundaries

Changing a configured group does not garbage-collect removed outputs.
[Automatic clean](040-automatic-clean.md) records historical production separately
and requires renewed deletion authority; changed rules or output membership
withhold automatic removal. Authored cleanup recipes must likewise restrict
removal to previously owned, validated member paths, preserve unrelated files
and reject paths outside the producer's designated root. Do not recursively
delete a shared directory or infer ownership from a glob.

Neither manifests nor directory paths define runtime output sets. An API claiming
runtime output ownership must define a persisted producer/member inventory, canonical collision
checks before publication, per-member content/existence validation, membership
invalidation for downstream users, atomic successful inventory replacement, and
cleanup limited to former owned members (never unrelated files). A plain manifest
or directory declaration does not currently promise those guarantees.
