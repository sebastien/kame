# Distribution and Bootstrap

## Purpose

This specification defines how Kame releases are published, how the `bin/kame`
launcher provisions and runs the right build for the host, and how a bootstrap
`Makefile` lets an existing `make` project delegate to Kame.

The goal is drop-in usage: put `kame` on `PATH`, or drop the bootstrap
`Makefile` into a project, and it works without a manual toolchain.

This specification governs release artifacts, the launcher, and the bootstrap
file. The native CLI contract is `009-cli.md`; the WebAssembly module and its
JavaScript wrapper are `010-wasm.md`.

## Release Artifacts

A release is a tag `vVERSION` whose `VERSION` matches the tagged tree. Each
release publishes these flat assets under the base URL
`https://github.com/sebastien/kame/releases/download/vVERSION/`:

| Asset | Built by | Role |
| --- | --- | --- |
| `kame.com` | `make dist-ape` | Cosmopolitan APE, full native CLI |
| `kame.wasm` | `make wasm` | freestanding module |
| `kame.js` | packaged JS CLI | Node CLI wrapper for `kame.wasm` (`010-wasm.md`) |
| `bin/kame` | stamped launcher | host provisioning and dispatch |
| `Makefile.bootstrap` | bootstrap template | project-local make delegation and `kame init` |
| `PROVENANCE.json` | release builder | version, source revision and asset digests |
| `SHA256SUMS` | checksums | signed integrity manifest |
| `SHA256SUMS.sig` | release signer | Ed25519 signature over the exact manifest bytes |

Asset filenames carry no version; the release tag does. Native per-platform
binaries are not published initially: the APE covers supported hosts and the
WASM build covers JavaScript runtimes.

`SHA256SUMS` contains one `<64-hex-lowercase>  <asset>` line per asset, sorted
by asset name, using the conventional two-space separator.

## Integrity

Every downloaded asset is verified against `SHA256SUMS` before it is installed
or executed. The launcher first verifies the detached Ed25519 signature using a
public key embedded in the stamped launcher, then checks the provenance file
and selected assets against that manifest. The release operator supplies both
key files to `make dist-release`; neither is stored in the repository. A
missing signature verifier, invalid signature, missing manifest entry, or
checksum mismatch is fatal. A partial or unverified download never replaces a
cached artifact, and cached artifacts are checked again before reuse.

## Version Pinning

The launcher is pinned to one version. Valid version labels contain only ASCII
letters, digits, `.`, `+`, and `-`, preventing cache paths from escaping their
per-version root. It resolves that version in order:

1. `KAME_VERSION` when set and nonempty.
2. An embedded stamp present in the released `bin/kame` asset.
3. A `VERSION` file in the launcher's directory or its parent, used for a
   source checkout.

The launcher never queries the network to discover a newer release. Updating
means replacing the launcher or setting `KAME_VERSION`. This is deliberate: no
run has hidden network latency, and a project's pin is explicit.

## Cache

The launcher stores provisioned artifacts under a per-version cache root, the
first usable of:

1. `KAME_HOME` when set and nonempty.
2. `${XDG_DATA_HOME}/kame` when `XDG_DATA_HOME` is set and nonempty.
3. `$HOME/.local/share/kame`.

Layout:

```text
$CACHE/<version>/SHA256SUMS
$CACHE/<version>/ape/kame.com
$CACHE/<version>/wasm/kame.js
$CACHE/<version>/wasm/kame.wasm
$CACHE/<version>/.backend
```

No usable cache root is a fatal provisioning error. The version directory is
created with owner-only permissions. A populated backend is reused without
network access.

## Backend Selection

`KAME_BIN`, when set, names an executable used directly with no detection and
no download. A `KAME_BIN` that is not executable is fatal.

`KAME_BACKEND` selects the strategy: `auto` (default), `ape`, or `wasm`.

- `ape` uses the APE.
- `wasm` uses `kame.js` under a JavaScript runtime.
- `auto` prefers `ape` when the host is expected to run it and otherwise
  falls back to `wasm`. When the host is expected to run the APE but an
  execution probe fails, `auto` proceeds to `wasm`.

APE hosts are the `uname -s`/`uname -m` pairs Cosmopolitan supports: Linux,
Darwin, FreeBSD, NetBSD, OpenBSD, and the Windows POSIX layers (`MINGW*`,
`MSYS*`, `CYGWIN*`), on `x86_64`/`amd64` and `aarch64`/`arm64`.

The `wasm` backend requires a JavaScript runtime. `KAME_JS` names it;
otherwise `node` is used. The runtime must be Node 18 or later.

The launcher may cache its backend decision per version in `.backend` so an
execution probe runs at most once. A cached decision whose artifact is missing
triggers provisioning again.

## Provisioning

The launcher provisions only the selected backend for the pinned version, and
only when its artifacts are absent. `KAME_NO_DOWNLOAD=1` forbids all network
access, so a missing artifact is then fatal rather than downloaded.
`KAME_RELEASE_URL` overrides the release base URL for self-hosting and tests.

Downloads use `curl` or `wget`. Concurrent launchers serialize on a lock in the
cache root, and a lock holder that dies must not deadlock later runs. Artifacts
are fetched into a staging directory, verified against `SHA256SUMS`, and moved
into place as a single atomic rename, so a failed download leaves the previous
cache state intact.

The launcher is silent on success. `KAME_VERBOSE=1` narrates provisioning on
stderr. The launcher never writes to stdout except for its own `--version`
answer.

## Dispatch

After resolving the program, the launcher replaces itself with it:

- APE: `exec "$cache/ape/kame.com" "$@"`.
- WASM: `exec "$js" "$cache/wasm/kame.js" "$@"`, with `kame.wasm` resolved
  beside `kame.js`.

`exec` is required so the CLI receives the terminal directly, owns its process
group, and passes its signals and exit status through unchanged.

A lone `-V` or `--version` argument is answered by the launcher from the pinned
version as `kame VERSION` on stdout with status 0, without provisioning. Any
other argument, including a `--version` used after `--` or as an option value,
is forwarded.

## Launcher Failures

The launcher runs before any Kame program exists and therefore cannot emit
structured diagnostics. It writes one `kame: <message>` line to stderr and
exits 1. These messages are outside the diagnostic registry of
`011-diagnostics.md` and never change the CLI's own diagnostics or statuses.
Each message names the failing stage: unsupported host, no JavaScript runtime,
no downloader, no verifier, missing manifest entry, checksum mismatch, cache
unavailable, or download failed.

## Bootstrap Makefile

A release ships a signed `Makefile.bootstrap` template. `kame init` verifies
and copies it to the current directory as a sidecar. It never changes
`Makefile`, `Makefile.kmk`, or another project file. If `Makefile.bootstrap`
already exists, init exits 1 and leaves it byte-for-byte unchanged. The
command pins `KAME_VERSION` to the launcher's resolved version. Projects can
run `make -f Makefile.bootstrap TARGET...` without changing an existing
Makefile, or inspect/include the sidecar themselves. The template contract:

- Variables: `KAME_VERSION` (default: the stamped version), `KAME_HOME` and
  `KAME_RELEASE_URL` (optional), and `KAME` (default `./bin/kame`).
- The first invocation provisions `./bin/kame` by downloading the stamped
  launcher for `KAME_VERSION`, verifying it against `SHA256SUMS`, and marking
  it executable. Provisioning is idempotent.
- Every requested goal is `.PHONY`, depends on the provisioned launcher, and is
  delegated in order: `@exec ./bin/kame $@`.
- A no-goal invocation delegates `default`.
- The delegate's exit status is the build's exit status.
- The template is self-contained POSIX `make` and `sh`; it needs only the same
  downloader and verifier tools as the launcher.
- `kame init` needs a verified cache/release manifest, creates its sidecar
  atomically without clobbering a concurrent or pre-existing path, and reports
  the make command needed to use it.

Documented limitations:

- GNU make command-line variable assignments (`make FOO=bar`) and the flags
  `-j`, `-n`, and `-k` are not forwarded; use `kame` directly for those.
- Goals are forwarded as target names; recipe-level arguments are not
  supported.
- Only GNU make and POSIX `sh` are supported.

## Packaging

Release assets are built with these targets, additive to the existing
`Makefile`/`Makefile.kmk`:

- `dist/kame.wasm` copies the `wasm` build output to `dist/`.
- `dist/kame.js` packages the JS CLI source.
- `bin/kame` is the checked-in launcher; a source checkout resolves its
  version from `VERSION`.
- `release` stages `dist/release/` with the exact asset names and a generated
  `SHA256SUMS`. Publishing is a manual, out-of-band step.

The JS CLI source lives in the repository and is copied unchanged to
`dist/kame.js`. Command execution keeps the absolute-path discipline of the
existing build rules.

## Acceptance Tests

- The launcher resolves a supported APE host, provisions it once, and preserves
  argv and exit status.
- The launcher selects `wasm` when the host is unsupported for APE or the APE
  probe fails.
- `KAME_BACKEND` forces a strategy; `KAME_BIN` bypasses detection and download.
- A missing manifest entry, checksum mismatch, invalid or wrong-key signature,
  provenance tampering, and missing OpenSSL verifier each fail closed and leave
  no installed artifact.
- A second invocation with a populated cache performs no network access.
- `KAME_NO_DOWNLOAD=1` fails when the artifact is absent.
- A lone `--version` prints `kame VERSION` and provisions nothing.
- An invalid version override fails before creating or accessing a cache path.
- `kame init` writes a correctly stamped bootstrap sidecar, preserves an
  existing `Makefile`, and refuses a second write without changing either file.
- Concurrent first invocations install exactly one verified artifact.
- The bootstrap Makefile provisions once and forwards single, multiple, and
  default goals in order with the delegate's exit status.
- Cache root selection honors `KAME_HOME`, then `XDG_DATA_HOME`, then `HOME`.

## Deferred

- Auto-update or a latest channel; pinning is intentional.
- Native per-platform release binaries.
- Windows-native (`cmd.exe`, PowerShell) launcher.
- Package-manager integrations.
