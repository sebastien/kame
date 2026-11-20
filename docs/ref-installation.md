# Installation and backends

Kame runs as a native Go CLI on Linux and macOS or as a JavaScript CLI backed
by WebAssembly. A Windows launcher bundle includes the JavaScript and WASM
runtime files. For a source build, use the
[contributor guide](ref-contributing.md).

## Choose a backend

| Form | Requirements and use |
| --- | --- |
| Source-built `dist/kame` | Go toolchain at build time. The native binary runs without Node. |
| Released native binary | Linux or macOS, on x86-64 or ARM64. Rename the downloaded binary to `kame`. |
| Released `kame-windows-x64.zip` | Windows x64 and Node.js 18+. Keep all extracted files together and run `kame.exe`. |
| Released `kame.js` + `kame.wasm` | Node.js 18+; keep both files adjacent and rename `kame.js` to `kame.mjs`, or set `"type": "module"` in their directory's `package.json`. |
| npm package | Node.js 18+ and npm; packages the WASM pair. |

Check [remaining host validation](../TODO.md) and the corresponding CI run for
platform-specific coverage.

### Automatic cleanup support

Ownership-aware `do clean` is implemented by the native POSIX host, with Linux
acceptance coverage. It requires descriptor-relative filesystem operations and
project locking.

The Node/WASM CLI (including npm) rejects `do clean`, including its `--dry-run`,
`--keep` and `--release` forms, with exit status 1; files and ownership records
remain intact. An authored `clean` is an ordinary task on either backend, never
an implicit target or fallback for `do clean`. Cache eviction through
`do cache clean` is separate.

## Verify a release before running it

Choose an existing tag from [GitHub Releases](https://github.com/sebastien/kame/releases).
`VERSION` in a checkout and locally staged assets do not establish publication.
The release contains four native binaries, the Windows bundle, the standalone
WASM pair, `VERSION`, `PROVENANCE.json`, `SHA256SUMS`, and `SHA256SUMS.sig`.

Obtain the release operator's Ed25519 **public** key in PEM format and verify its
fingerprint through an independently authenticated channel. No trusted key is
bundled in this checkout. Downloading a key beside the assets does not establish
trust. If you cannot authenticate a key, do not treat a matching checksum as
publisher authentication; use a source build from a revision you trust instead.

In a POSIX shell, set `KAME_VERSION` to the published version **without** `v`,
and `KAME_RELEASE_PUBLIC_KEY` to the absolute path of that trusted PEM file.
With curl, OpenSSL supporting Ed25519 `pkeyutl -rawin`, and a SHA-256 utility:

```sh
set -eu
version=${KAME_VERSION:?Set KAME_VERSION to a published version}
public_key=${KAME_RELEASE_PUBLIC_KEY:?Set an absolute trusted PEM key path}
base="https://github.com/sebastien/kame/releases/download/v$version"
mkdir kame-download
cd kame-download
for asset in kame-linux-x86_64 kame-linux-arm64 \
  kame-darwin-x86_64 kame-darwin-arm64 kame-windows-x64.zip \
  kame.js kame.wasm VERSION PROVENANCE.json SHA256SUMS SHA256SUMS.sig; do
  curl -fL "$base/$asset" -o "$asset"
done
openssl pkeyutl -verify -pubin -inkey "$public_key" -rawin \
  -in SHA256SUMS -sigfile SHA256SUMS.sig
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum --check SHA256SUMS
else
  shasum -a 256 --check SHA256SUMS
fi
test "$(cat VERSION)" = "$version"
```

Stop on any failure. After verification, select your platform binary. For
example, on Linux x86-64:

```sh
mv kame-linux-x86_64 kame
chmod +x kame
./kame --help
```

Or run the verified WASM pair:

```sh
mv kame.js kame.mjs
node ./kame.mjs --help
```

For npm, substitute an actually published package version:
`npm x --yes @ssebastien/kame@VERSION -- --help`. This uses npm's distribution
path; it does not perform the detached GitHub manifest verification above.
Homebrew/Scoop definitions live in [`etc/`](../etc/README.md), not release assets.

## Source-tree launcher and Make delegation

`bin/kame` downloads only verified runtime assets; it never updates itself.
From the checkout root, with a published version and the same trusted PEM key:

```sh
export KAME_VERSION
export KAME_RELEASE_PUBKEY_B64_OVERRIDE="$(openssl pkey -pubin \
  -in "$KAME_RELEASE_PUBLIC_KEY" -outform DER | openssl base64 -A)"
bin/kame --help
KAME_NO_DOWNLOAD=1 bin/kame --help
```

The first execution needs curl/wget, OpenSSL, and `sha256sum`/`shasum`.
`KAME_BACKEND=native|wasm` overrides automatic selection. WASM needs Node.
`KAME_HOME` selects the launcher cache, otherwise XDG data home or
`$HOME/.local/share/kame` is used. Concrete versions reuse verified cached
assets offline. `KAME_VERSION=latest` requires a selector download on each
invocation, even with a populated cache. A lone `--version` does not verify or
provision runtime assets; use `--help` to test installation.

`kame init` is retired. Copy `Makefile` from a checkout next to your
`Kamefile`: `make GOAL...` uses `KAME`, a matching `kame` on `PATH`, or installs
the pinned native GitHub release into `.kame/bootstrap/`, then forwards goals to
it. Pin with `KAME_VERSION` or a `VERSION` file. No verification key is
required; set `KAME_RELEASE_PUBLIC_KEY` to also check the signature. Forwarding
limits and the operator publication procedure are defined in the
[distribution specification](spec/015-distribution.md).
