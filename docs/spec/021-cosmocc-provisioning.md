# Verified Cosmocc provisioning

## Contract

The source build provisions Cosmocc 4.0.2 from the versioned archive
`https://cosmo.zip/pub/cosmocc/cosmocc-4.0.2.zip`. The archive SHA-256 is
`85b8c37a406d862e656ad4ec14be9f6ce474c1b436b9615e91a55208aced3f44`.
The value is pinned from the published `cosmo-build` 3.0.0 toolchain source and
must be checked before extraction. `COSMOCC_URL` may select a mirror of the
same bytes. Deliberately selecting another archive requires an explicit
`COSMOCC_SHA256` override.

Downloads are staged beside the destination. A digest mismatch, invalid digest,
missing verifier, failed extraction, or absent executable must leave the
installation path absent. A successful install carries an `.archive-sha256`
marker. An existing compiler without a matching marker is rejected so an older
unverified directory cannot silently bypass the new check. Make targets use a
versioned destination to avoid reusing the previous mutable-download cache.

## Acceptance

- The default URL names the pinned release and the default digest is fixed.
- A valid local archive installs with its digest marker and reuses the verified
  installation without contacting the source again.
- A mismatching archive fails before extraction and leaves no destination.
- Both Makefile build descriptions use the versioned destination and compiler.

This specification covers compiler archive provisioning. Signed release
manifests and provenance attestations follow `015-distribution.md`.
