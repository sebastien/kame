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
| `VERSION` | release tree | signed version selector for the explicit latest channel |
| `PROVENANCE.json` | release builder | version, source revision and asset digests |
| `SHA256SUMS` | checksums | signed integrity manifest |
| `SHA256SUMS.sig` | release signer | Ed25519 signature over the exact manifest bytes |

Asset filenames carry no version; the release tag does. Only `kame.com`,
`kame.js` and `kame.wasm` are runtime downloads. On supported Unix hosts, use
`chmod +x kame.com` then `./kame.com --help`; on Windows rename it to `kame.exe`
and run `.\kame.exe --help`. For the WASM CLI, keep `kame.js` and `kame.wasm`
adjacent, rename `kame.js` to `kame.mjs` so Node recognizes the ES module, and
run `node ./kame.mjs --help` with Node.js 18+, or use
`npm x --yes @ssebastien/kame@VERSION -- --help`.
The Cosmopolitan APE provides the native CLI on its supported hosts.
The pinned Cosmopolitan 4.0.2 loader is incompatible with OpenBSD 7.5 and later:
its syscall entry points are rejected by `pinsyscalls(2)`. Those OpenBSD
releases should use the Node/WASM pair instead.
Manual native CI outputs and Windows bundles remain available for
development/testing, but are never collected by `dist-release`. Neither platform
placeholders nor stale outputs under `dist/native/` may enter a release.
The Homebrew formula and Scoop manifest live in `etc/kame.rb` and
`etc/kame.json`; both pin the version and digest of `kame.com`. Scoop renames
that download to `kame.exe`. They are not release attachments.

The launcher and bootstrap sections below describe source-tree helpers.
Releases do not ship the launcher. `init` and launcher self-update are
retired; the checked-in launcher provisions only runtime assets. The repository
`Makefile` is the bootstrap and is copied manually from a source checkout.

`SHA256SUMS` contains one `<64-hex-lowercase>  <asset>` line per asset, sorted
by asset name, using the conventional two-space separator.

## Integrity

Every downloaded asset is verified against `SHA256SUMS` before it is installed
or executed. The launcher first verifies the detached Ed25519 signature using a
trusted public key configured through `KAME_RELEASE_PUBKEY_B64_OVERRIDE`
(or embedded in a legacy launcher), then checks the provenance file
and selected assets against that manifest. The release operator supplies both
key files to `make dist-release`; neither is stored in the repository. The
GitHub release helper fetches `package.signing.pem` and
`package.verification.pem` with littlesecrets when key-path environment
overrides are not supplied. A
missing signature verifier, invalid signature, missing manifest entry, or
checksum mismatch is fatal. A partial or unverified download never replaces a
cached artifact, and cached artifacts are checked again before reuse.

### WASM identity reporting

Release provenance is schema-1 JSON with `version`, `sourceRevision`, `created`,
and `subjects`; each subject has an asset `name` and lowercase SHA-256 `sha256`.
`created` is the release assembly time, not the compilation time. The builder
must describe the revision used to produce the staged assets, not merely the
checkout revision at a later signing step.

For JavaScript JSON version reporting, look for `PROVENANCE.json` beside the
wrapper, then in its parent directory (the versioned launcher-cache root).
Use the first existing candidate; an invalid or unreadable candidate does not
authorize searching elsewhere for a more convenient identity. Do not search
the project's cwd or fetch metadata. Flat release installations retain adjacent
provenance; launcher installations retain it at the versioned cache root.

An identity match requires a valid schema-1 document, a concrete version,
a nonempty, non-`unknown` source revision, and unique, valid digest entries for
both `kame.js` and `kame.wasm`. Hash the running wrapper and the actual selected
module, including a `KAME_WASM` override, against those entries. Renaming the
wrapper to `kame.mjs` does not change its canonical subject name or byte digest.
Both must match before advertising the provenance version and source revision
through the existing JSON `version` and `buildID` fields. Neither `KAME_VERSION`
nor an unrelated `VERSION` file can supply or override that matched identity.

Missing, unreadable, malformed, unsupported, incomplete or mismatched provenance,
or an unavailable selected asset, leaves `buildID` and `buildTime` unknown;
the version query still succeeds without instantiating the module. Retain the
ordinary version-label fallback when identity is unavailable. Reporting performs
only local reads and hashes, never installation, cache writes, network requests
or process execution. Human short-format version queries remain available
without the module or provenance.

Matching local metadata establishes byte correspondence, not publisher
authenticity: replacing both assets and provenance can produce another local
match. Authentication belongs to signed-manifest verification during
provisioning and reuse. That verification remains mandatory and fails closed;
an unknown identity response cannot bypass it. SDK consumers pin a signed
release or verified artifact digests, not merely a displayed version label.

## Version Pinning

The launcher uses a pinned version by default. Valid version labels contain
only ASCII letters, digits, `.`, `+`, and `-`, preventing cache paths from
escaping their per-version root. It resolves that version in order:

1. `KAME_VERSION` when set to a concrete version.
2. An embedded version stamp in a legacy launcher, if present.
3. A `VERSION` file in the launcher's directory or its parent, used for a
  source checkout.

Setting `KAME_VERSION=latest` opts into a moving channel. Unless `KAME_BIN` is
set, the launcher fetches the plain-text `VERSION` selector from
`KAME_LATEST_URL` (default:
`https://github.com/sebastien/kame/releases/latest/download/VERSION`) on each
invocation, validates the resolved value as a concrete version, then uses that
version's normal signed release manifest and cache. The selector only chooses
which immutable release to verify; it does not bypass signature or checksum
verification. `KAME_RELEASE_URL` still controls the selected release's asset
base URL. The latest selector requires downloads and fails when
`KAME_NO_DOWNLOAD=1`. `--version` may fetch the selector but provisions no
runtime or launcher artifacts. Concrete `KAME_VERSION` values and the
launcher's stamped version remain pinned and perform no selector request.

The latest selector changes only the runtime version: the launcher verifies
that release's signed manifest and provenance, then provisions the selected
APE or WASM pair. It never downloads or executes a replacement shell launcher.
`KAME_BIN` bypasses selection. WASM dispatch exports the resolved concrete
`KAME_VERSION`, not the literal `latest` selector.

## Cache

The launcher stores provisioned artifacts under a per-version cache root, the
first usable of:

1. `KAME_HOME` when set and nonempty.
2. `${XDG_DATA_HOME}/kame` when `XDG_DATA_HOME` is set and nonempty.
3. `$HOME/.local/share/kame`.

Layout:

```text
$CACHE/<version>/SHA256SUMS
$CACHE/<version>/SHA256SUMS.sig
$CACHE/<version>/PROVENANCE.json
$CACHE/<version>/ape/kame.com
$CACHE/<version>/wasm/kame.js
$CACHE/<version>/wasm/kame.wasm
$CACHE/<version>/wasm/package.json
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
Darwin, FreeBSD, NetBSD, and the Windows POSIX layers (`MINGW*`,
`MSYS*`, `CYGWIN*`), on `x86_64`/`amd64` and `aarch64`/`arm64`.
`src/sh/test-ape.sh` runs the APE through the POSIX shell adapter,
checks version output, executes a recipe with an explicit environment, and
checks timeout cleanup. `.github/workflows/ape-platforms.yml` builds the APE and
WASM pair once on Ubuntu and tests the same artifacts downstream. It runs the
APE shell test on Linux, Darwin, FreeBSD and NetBSD, and the PowerShell native
CLI acceptance test on Windows. Windows also runs the packaged Node/WASM CLI.
The build job retains Linux core/host tests, WASM ABI and CLI checks, and signed
release packaging tests with ephemeral test keys; it never publishes a release.

The `wasm` backend requires a JavaScript runtime. `KAME_JS` names it;
otherwise `node` is used. The runtime must be Node 18 or later. The launcher
writes a local `package.json` containing `{"type":"module"}` in its WASM cache
before dispatch, so the verified `kame.js` works without relying on Node's
syntax detection or the surrounding project's module type.

The Windows bundle's `kame.exe` locates `kame.js` beside itself and starts Node
with the original argument vector, inherited standard streams, environment,
working directory, and exit status. `KAME_NODE` may select a specific Node
executable; by default the launcher resolves `node` through `PATH`.

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
version as `kame VERSION (runtime=unknown)` on stdout with status 0, without
selecting or provisioning a backend. Any
other argument, including a `--version` used after `--` or as an option value,
is forwarded.
The spelling `unknown` follows the [canonical runtime table](009-cli.md#runtime-names)
and means no backend was selected, not an unknown artifact build mode. This
short format is an intentional no-provision exception to native version output;
combined options such as `--json --version` are forwarded to the selected CLI.

## Launcher Failures

The launcher runs before any Kame program exists and therefore cannot emit
structured diagnostics. It writes one `kame: <message>` line to stderr and
exits 1. These messages are outside the diagnostic registry of
`011-diagnostics.md` and never change the CLI's own diagnostics or statuses.
Each message names the failing stage: unsupported host, no JavaScript runtime,
no downloader, no verifier, missing manifest entry, checksum mismatch, cache
unavailable, or download failed.

## Bootstrap Makefile

The repository `Makefile` is the bootstrap. It is not a release attachment.
Copy it into a project to replace that project's Makefile. `bin/kame init`,
including its help forms, exits 1 with migration guidance before version
selection, downloads or project writes. The contract:

- Variables: `KAME_VERSION` (default `0.3.1`), `KAME_RELEASE_URL` (default that
  version's GitHub release), and `KAME` (default
  `$(HOME)/.cache/kame/bin/kame-$(KAME_VERSION)`).
- The first run downloads `kame.com` from the author's GitHub release with
  curl, wget, or fetch, marks it executable, and caches it. Later runs reuse
  the cache and do not download. There is no signature check: the asset host is
  the author's repository. A failed download leaves no executable. The bootstrap
  does not use the launcher's backend selection.
- Every requested goal is delegated via `@exec "$(KAME)" $@`. A no-goal
  invocation delegates `default`. The delegate's exit status is the build's.
- BSD make, GNU make, and POSIX `sh`.

Documented limitations:

- Each goal is a separate `kame` invocation, in order, with every command-line
  binding (`make CC=clang CFLAGS=-O0 foo bar`). Make's own flags (`-j`, `-n`,
  `-k`) are not forwarded.
- `KAME`, `KAME_VERSION`, and `KAME_RELEASE_URL` configure the bootstrap and are
  not passed on. Recipe-level arguments are not supported.

## Packaging

Release assets are built with these `Makefile.kmk` targets (`make` forwards them):

- `dist/kame.wasm` copies the `wasm` build output to `dist/`.
- `dist/kame.js` packages the JS CLI source.
- `bin/kame` is the checked-in launcher; a source checkout resolves its
  version from `VERSION`.
- `dist-release` stages only `kame.com`, `kame.js`, `kame.wasm`, `VERSION`,
  provenance and the signed checksum manifest. It does not collect native
  platform artifacts or generate package definitions. The signer rejects
  unexpected files, directories and symlinks in the release directory.
- `make package-manifests` explicitly refreshes `etc/kame.rb` and
  `etc/kame.json` using `dist/kame.com` and `VERSION`; commit these definitions
  before publication. Release staging never modifies tracked files.
- `make release` builds the runtime assets and runs the manual publication
  wrapper below. It performs preflight before keys are read, then signs and
  publishes the prebuilt assets; `dist-release` remains the staging-only task.
- `dist-native` builds the current host's full native CLI and places it at
  `dist/native/PLATFORM/kame`; the manual-only
  `.github/workflows/native-artifacts.yml` uploads Linux and macOS x64/ARM64
  development builds, not release attachments. This toolchain matrix runs only
  through `workflow_dispatch`, not on pushes or pull requests; its success is
  not required to build the portable distribution. There is no separate BSD
  native-artifact workflow; BSD distribution uses the APE or WASM artifacts.

Install the Scoop definition from a source checkout:

```sh
scoop install ./etc/kame.json
```

For Homebrew, copy `etc/kame.rb` into a tap's `Formula/` directory and install
from that tap. Both definitions expose the APE as `kame`. Updating them does
not automatically publish a tap or bucket.

The JS CLI source lives in the repository and is copied unchanged to
`dist/kame.js`. Command execution keeps the absolute-path discipline of the
existing build rules.

Source-tree helper scripts live under `src/sh/`: build and provisioning helpers
(`generate-version.sh`, `provision-cosmocc.sh`, `build-ape.sh`,
`check-portable.sh`), release helpers (`platform_id.py`, `release_manifest.py`,
`release_package_manifests.py`, `release.sh`) and harness helpers
(`benchmark-cli.py`, `test-ape.sh`, `test-release-build.sh`). The Go/WASM/C and
Node tools stay under `tools/`.

## Manual Publication

Publishing is an explicit operator step; no workflow publishes a release.
`make release` (or `kame release`) builds the runtime assets and runs
`src/sh/release.sh`, which verifies preflight, then wraps the signed staging and
the GitHub CLI:

```sh
KAME_RELEASE_PUBLIC_KEY=verification.pem \
KAME_RELEASE_SIGNING_KEY=signing.pem \
  make release
```

The wrapper's optional flags are named arguments:
`make release repo=OWNER/REPO` or `make release notes=NOTES.md`. They are
forwarded as `--repo` and `--notes-file`; key paths still come from the
environment.

For a new release, before retrieving signing keys or signing, it requires a clean tracked tree,
checks that the exact HEAD commit exists in the target GitHub repository, and
refuses to overwrite an existing local/remote tag or draft release. The operator must push
the commit first, including when releasing from a detached HEAD; the script
never pushes code automatically. Authentication/API failures stop preflight.
It stages and signs `dist/release/`, creates a draft release for the `VERSION`
tag, uploads the seven flat runtime/verification assets, verifies that every manifest
asset is present, and only then publishes. `--repo` selects
another repository and `--notes-file` supplies release notes in place of
generated change notes. Both paths always include download descriptions, APE
and Node/WASM commands, verification guidance and a version-pinned
`npm x --yes @ssebastien/kame@VERSION -- --help` example. The signing key material is supplied out of band and is never
stored in the repository.

If the GitHub release is already published, rerunning the same command leaves
GitHub untouched and checks the exact npm package version on npmjs.org. An
existing npm version is a successful no-op; a missing version retries npm only,
using the released `kame.js`/`kame.wasm` and the tagged `package.json`, README and
license in a temporary directory. Recovery needs neither a clean checkout nor
signing keys, and never publishes possibly newer local artifacts. Registry/auth
failures other than a missing-version E404 stop without publishing. Successful
npm publication is checked against the registry. After npm accepts an upload,
temporary E404s are retried every ten seconds for up to five minutes without
repeating the upload. If still unavailable, exit status 3 reports accepted but
still processing and advises checking `npm view`, not republishing. Other
verification errors fail immediately. Failed authentication/2FA during the
upload can be corrected and retried without bumping VERSION or deleting the
GitHub release.

The `Makefile.kmk` staging task delegates to the same signed GNU Make recipe.

`src/sh/build-ape.sh` builds `dist/kame.com` without GNU Make by wrapping the
pinned Cosmocc provisioning and the release build flags. It is a bootstrap path
for a checkout with no working `make`; the `Makefile`/`Makefile.kmk` rules remain
the canonical build.

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
- Provisioned WASM assets retain signed provenance for local identity reporting.
  JSON version output matches both selected assets, including wrapper renaming
  and module overrides; missing or mismatched local reporting metadata yields
  unknown identity without instantiation or downloads. Tampering still fails
  normal provisioning/reuse verification rather than downgrading its checks.
- `KAME_NO_DOWNLOAD=1` fails when the artifact is absent.
- A lone `--version` prints `kame VERSION (runtime=unknown)` and provisions nothing.
- `KAME_VERSION=latest` resolves the selector, prints its concrete version
  without provisioning for `--version`, and provisions only that version for
  execution; disabled downloads fail before creating the cache.
- `KAME_VERSION=latest` provisions the selected runtime without fetching a
  replacement shell launcher. Minimal releases contain no shell/bootstrap assets.
- `kame-windows-x64.zip` contains exactly `kame.exe`, `kame.js`, and
  `kame.wasm`; the native launcher preserves argv, standard streams, working
  directory, environment and the Node process exit status. On a Windows host,
  run `node tools/windows/test-launcher.mjs dist/windows` after
  `make dist-windows`; it exercises the packaged CLI, launcher contract, recipe
  timeout and descendant-process reaping.
  `.github/workflows/ape-platforms.yml` builds the bundle alongside the portable
  release artifacts and runs this test on `windows-latest`. Passing a
  cross-compile alone does not establish Windows-host conformance.
- `kame-windows-x64.exe` runs directly on Windows without Node and satisfies
  the native CLI contract in 035. Acceptance includes version and PowerShell
  recipes, 64 KiB pipeline writes, EOF closure, 2 MiB transfer, cache, watch and
  descendant cleanup on an actual Windows host.
- An invalid version override fails before creating or accessing a cache path.
- Retired `kame init`, including help forms, reports migration guidance without
  network access, cache creation or project writes.
- Concurrent first invocations install exactly one verified artifact.
- The bootstrap Makefile downloads once and forwards single, multiple, and
  default goals in order with the delegate's exit status, on BSD make and GNU make.
- A failed bootstrap download installs nothing and does not execute.
- Cache root selection honors `KAME_HOME`, then `XDG_DATA_HOME`, then `HOME`.
- Linux native package/host tests run automatically. Additional native source
  builds on Linux and macOS x64/ARM64 are manual development checks. The native
  artifact workflow retains hosted
  execution results; disabled automatic triggers and unrun jobs are not
  conformance evidence.
- APE host conformance runs a minimal standalone Cosmopolitan executable
  before the Kame CLI checks, so loader failures are distinct from application
  startup failures on each tested host. OpenBSD uses the Node/WASM fallback
  because Cosmopolitan 4.0.2 APEs fail OpenBSD syscall pinning. BSD native source
  builds are not covered by CI and are not a portable-release gate.
- A published release can build the current checkout's `dist/kame` through the
  npm/WASM CLI (`bunx`/`npx`) or the latest GitHub APE. The opt-in
  `src/sh/test-release-build.sh` tries WASM first, falls back to the APE, and
  fails only when neither channel self-builds;
  `.github/workflows/release-self-build.yml` runs it on demand, weekly, and after
  a release. It stays outside the hermetic suite because it requires network
  access and the Solod toolchain.
- The portable build workflow separately requires the current WASM module to
  bootstrap `dist/kame` under its unchanged 16 MiB instance budget, then recheck
  warm inputs in the same module without linear-memory growth. This hard gate
  uses `tools/wasm/test-self-build.mjs`, not the published-channel fallback:
  an older release's known WASM limitations cannot hide current regressions.
