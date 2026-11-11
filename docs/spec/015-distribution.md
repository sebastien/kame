# Distribution

## Purpose

Kame ships standalone Go native binaries and a Node/WebAssembly backend. The
source-tree `bin/kame` launcher selects, verifies and caches a pinned release.
The repository `Makefile` builds directly with Go; `Makefile.kmk` supports
self-builds with Kame. Neither build requires Solod or a C compiler.

## Release Artifacts

A release is a tag `vVERSION` matching the version in its committed tree. Flat
assets are published under
`https://github.com/sebastien/kame/releases/download/vVERSION/`.

| Asset | Role |
| --- | --- |
| `kame-linux-x86_64` | Linux amd64 native CLI |
| `kame-linux-arm64` | Linux ARM64 native CLI |
| `kame-darwin-x86_64` | macOS Intel native CLI |
| `kame-darwin-arm64` | macOS Apple Silicon native CLI |
| `kame-windows-x64.zip` | Windows Go launcher, JS/WASM pair and ESM package metadata |
| `kame.js`, `kame.wasm` | Node CLI and Go WASI preview-1 reactor |
| `VERSION` | Pinned release version |
| `PROVENANCE.json` | Version, source revision, creation time and asset digests |
| `SHA256SUMS` | Sorted SHA-256 manifest including provenance |
| `SHA256SUMS.sig` | Ed25519 signature over the exact checksum manifest |

Native binaries use `CGO_ENABLED=0`. Download the matching platform binary,
rename it to `kame`, make it executable and run `./kame --help`.
There is no APE or `kame.com` artifact. Other systems can use Node/WASM where
Node and the required host capabilities are available.

The Windows archive must be extracted together. Its `kame.exe` launcher invokes
Node with the adjacent `kame.js`; Node 18+ is required. `package.json` identifies
that wrapper as an ES module. The standalone JS/WASM downloads must also stay
adjacent; rename the wrapper to `kame.mjs`, or use a directory with ESM package
metadata. npm ships the same JS/WASM runtime pair.

`make release-assets` cross-builds all four native binaries and the Windows
archive, and builds the WASM pair. `make dist-native` stages the current host
binary under `dist/native/OS-ARCH/kame`. Those development directories are not
release attachments. `make dist-release` stages exactly the required flat assets
and signs them; missing files, symlinks or unexpected release assets are errors.

## Integrity and identity

Every launcher download is checked against `SHA256SUMS`. Before trusting it,
the launcher verifies `SHA256SUMS.sig` with an operator-configured trusted
Ed25519 public key, then verifies the provenance and selected asset digests.
Source launchers accept `KAME_RELEASE_PUBKEY_B64_OVERRIDE`; release signing uses
`KAME_RELEASE_PUBLIC_KEY` and `KAME_RELEASE_SIGNING_KEY`. Keys are not committed.
A checksum without a verified signature does not authorize execution.

The manifest contains `<64 lowercase hex digits>  <asset>` lines sorted by name.
Provenance is covered by that manifest. Runtime identity comes from build
metadata or verified adjacent provenance, never merely a requested environment
version selector. The native CLI reports runtime `Go`; the JS wrapper reports
runtime `WASM`.

## Version and cache

The launcher resolves `KAME_VERSION`, a legacy embedded stamp, then the adjacent
`VERSION`. `latest` explicitly reads the latest release's version asset. Version
syntax is validated before using it in a path. A lone `--version` or `-V` request
answers without provisioning. `KAME_BIN` bypasses detection and downloads after
validating that the selected executable exists.

Cache roots are `KAME_HOME`, `$XDG_DATA_HOME/kame`, then
`$HOME/.local/share/kame`. Each version has its own directory. Native executables
live at `VERSION/native/OS-ARCH/kame`; WASM files live at `VERSION/wasm/`.
The launcher installs ESM package metadata beside the cached JS file.

A per-version directory lock serializes provisioning. The lock records its PID,
reports contention and removes stale owners. Downloads go to a private staging
directory, are verified before publication, then move into the cache. Cached
assets are verified again before execution. Failed verification never executes
or publishes a partial install. `KAME_NO_DOWNLOAD=1` permits only already cached,
verified artifacts. `KAME_RELEASE_URL` overrides the release base for testing or
private distribution.

## Backend selection and dispatch

`KAME_BACKEND` accepts `native`, `wasm` or `auto` (default). Native selection
supports Linux and Darwin on x86-64 and ARM64, using `uname` to select the signed
asset. A forced native selection on another platform fails clearly.

Auto selection honors a usable cached choice, otherwise probes the matching
native executable with `--version`. A failed executable probe selects WASM;
integrity or download failures remain fatal. Unsupported native platforms use
WASM. `KAME_JS` selects the Node executable, whose version must be at least 18.

After provisioning the launcher releases its lock and `exec`s the selected
native binary or Node wrapper, preserving arguments, streams and exit status.
It never downloads or executes a replacement shell launcher. `init` and launcher
self-update are retired.

## Package manifests

`make package-manifests` updates `etc/kame.rb` and `etc/kame.json` separately from
release staging. Homebrew selects the OS and CPU-specific native binary and its
checksum. Scoop installs the Windows archive and declares its Node dependency.
Both definitions pin the release version and asset digest. They are source
artifacts, not signed release attachments.

## Publication

`src/sh/release.sh` is an explicit operator action. It validates arguments,
version agreement, repository state and remote release state before loading keys
or staging. Dirty tracked sources, conflicting tags, existing drafts and API
failures stop publication. Key paths can be provided directly or retrieved from
the configured secrets tool after preflight.

The script stages and verifies signed assets, creates a draft GitHub release,
uploads every staged file, then publishes it. It also publishes the npm runtime
package from immutable tagged metadata and uploaded WASM assets. A published
GitHub release can be resumed solely to finish missing npm publication; that
recovery neither rebuilds nor replaces release artifacts. Registry errors are
not treated as proof that an npm version is missing.

`Makefile.kmk`'s release task prebuilds assets while holding its project lease
and passes `KAME_RELEASE_PREBUILT=1`, so staging does not recursively enter Kame.
Release notes describe platform downloads, Node requirements and signature
verification. No build or test target publishes a release automatically.

## Verification

Go package tests, native process ownership tests, repeated in-process CLI tests,
native/WASM conformance, import-table inspection and signed fixture tests cover
the shipped implementations. Release-script tests mock GitHub, npm and secrets;
they must never contact a real publishing service. Native CI runs on Linux and
macOS; cross-compilation also verifies both supported CPU architectures and the
Windows launcher.
