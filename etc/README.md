# Package definitions

- `kame.rb`: Homebrew formula. Installs the version-pinned native binary as `kame`.
  Copy it to a Homebrew tap's `Formula/kame.rb` and install from that tap.
- `kame.json`: Scoop manifest. Extracts the Windows bundle, installs its Node
  dependency, and exposes `kame.exe` as `kame`. Install with `scoop install ./etc/kame.json`
  from a checkout, or publish it in a Scoop bucket.

These definitions are checked into the repository, not attached to releases.
Both pin the release version and the SHA-256 digest of each selected asset.
Run `make package-manifests` after setting `VERSION` to build the assets and
refresh these definitions. Commit them before publishing.

For an already published release, generate definitions from its verified
downloaded assets. Set `KAME_VERSION` to the release version without `v`.

```sh
kame do run --allow-read --allow-run --allow-write=./etc \
  src/kash/package-manifests.kash -- directory=./dist/release \
  output=./etc "version=$KAME_VERSION"
```
