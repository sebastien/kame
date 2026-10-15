# Package definitions

- `kame.rb`: Homebrew formula; installs the version-pinned APE as `kame`.
  Copy it to a Homebrew tap's `Formula/kame.rb` and install from that tap.
- `kame.json`: Scoop manifest; downloads the same APE, saves it as `kame.exe`,
  and exposes the `kame` command. Install with `scoop install ./etc/kame.json`
  from a checkout, or publish it in a Scoop bucket.

These definitions are checked into the repository, not attached to releases.
Both embed the exact version and SHA-256 digest of `kame.com`. Refresh them
with `make package-manifests` after setting `VERSION` and building the final
release APE, then commit them before publishing. This target updates `etc/`;
`dist-release` does not change tracked files.

For an already published release, generate definitions from its downloaded
`kame.com`, rather than rebuilding a potentially different binary:

```sh
python3 src/sh/release_package_manifests.py --directory dist/release \
  --output-directory etc --version 0.2.3
```
