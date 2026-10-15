# Installation and backends

Kame runs as a host-native CLI, a standalone Actually Portable Executable (APE),
or a JavaScript CLI backed by WebAssembly. The build language is shared; host
support and prerequisites differ. For a source build, use the
[contributor guide](ref-contributing.md).

## Choose a backend

| Form | Requirements and use |
| --- | --- |
| Source-built `dist/kame` | Native compiler toolchain at build time; no Node at runtime. |
| Released `kame.com` | APE-compatible host; no Node or Make at runtime. On Windows, rename to `kame.exe`. |
| Released `kame.js` + `kame.wasm` | Node.js 18+; keep both files adjacent and rename `kame.js` to `kame.mjs`, or set `"type": "module"` in their directory's `package.json`. |
| npm package | Node.js 18+ and npm; packages the WASM pair. |

APE targets include Linux, macOS, FreeBSD, NetBSD, and Windows on supported
Cosmopolitan architectures. The pinned Cosmopolitan 4.0.2 APE is incompatible
with OpenBSD 7.5+: use Node/WASM there. The table describes backend selection,
not proof of conformance on every host. Check [remaining host validation](../TODO.md)
and the corresponding CI run before depending on platform-specific behavior.

## Verify a release before running it

Choose an existing tag from [GitHub Releases](https://github.com/sebastien/kame/releases).
`VERSION` in a checkout and locally staged assets do not establish publication.
The seven flat release assets are the three runtime files, `VERSION`,
`PROVENANCE.json`, `SHA256SUMS`, and `SHA256SUMS.sig`.

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
for asset in kame.com kame.js kame.wasm VERSION PROVENANCE.json SHA256SUMS SHA256SUMS.sig; do
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

Stop on any failure. After verification, on a supported Unix APE host:

```sh
chmod +x kame.com
./kame.com --help
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
export KAME_RELEASE_PUBKEY_B64_OVERRIDE="$(openssl pkey -pubin \
  -in "$KAME_RELEASE_PUBLIC_KEY" -outform DER | openssl base64 -A)"
bin/kame --help
KAME_NO_DOWNLOAD=1 bin/kame --help
```

The first execution needs curl/wget, OpenSSL, and `sha256sum`/`shasum`.
`KAME_BACKEND=ape|wasm` overrides automatic selection; WASM also needs Node.
`KAME_HOME` selects the launcher cache, otherwise XDG data home or
`$HOME/.local/share/kame` is used. Concrete versions reuse verified cached
assets offline. `KAME_VERSION=latest` requires a selector download on each
invocation, even with a populated cache. A lone `--version` does not verify or
provision runtime assets; use `--help` to test installation.

`kame init` is retired. Copy `Makefile.bootstrap` from a trusted checkout and
configure its version/key to delegate GNU Make goals. Its forwarding limits,
launcher settings, and operator publication procedure are defined in the
[distribution specification](spec/015-distribution.md).
