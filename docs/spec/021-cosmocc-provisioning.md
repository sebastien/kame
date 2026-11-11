# Retired Cosmopolitan Toolchain

The Go migration retires Cosmopolitan/APE artifacts and compiler provisioning.
Native builds use the standard Go compiler with `CGO_ENABLED=0`; WASM builds
use `GOOS=wasip1 GOARCH=wasm` and `-buildmode=c-shared`.

Release integrity remains enforced by the signed asset manifest and provenance
in `015-distribution.md`. The native toolchain gate inspects Go build metadata;
release gates verify OS/architecture, absence of cgo and the Windows bundle.
No compiler archive is downloaded or executed by Kame's build files.
