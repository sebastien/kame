#!/usr/bin/env python3
"""Create a signed checksum manifest and provenance for a staged Kame release."""

import argparse
import hashlib
import json
import pathlib
import subprocess
import sys
from datetime import datetime, timezone


def run(*args):
    return subprocess.check_output(args, text=False)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", required=True, type=pathlib.Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--public-key", required=True, type=pathlib.Path)
    parser.add_argument("--signing-key", required=True, type=pathlib.Path)
    args = parser.parse_args()

    assets = ("VERSION", "kame.com", "kame.js", "kame.wasm", "kame-windows-x64.zip", "bin/kame", "Makefile.bootstrap", "Formula/kame.rb", "kame.json")
    for name in assets:
        if not (args.directory / name).is_file():
            parser.error(f"missing release asset: {name}")

    openssl = "openssl"
    public_der = run(openssl, "pkey", "-pubin", "-in", str(args.public_key), "-outform", "DER")
    signing_public_der = run(openssl, "pkey", "-in", str(args.signing_key), "-pubout", "-outform", "DER")
    if public_der != signing_public_der:
        parser.error("release public key does not match the signing key")

    subjects = []
    for name in assets:
        data = (args.directory / name).read_bytes()
        subjects.append({"name": name, "sha256": hashlib.sha256(data).hexdigest()})
    provenance = {
        "schema": 1,
        "version": args.version,
        "sourceRevision": args.revision,
        "created": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "subjects": subjects,
    }
    (args.directory / "PROVENANCE.json").write_text(
        json.dumps(provenance, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )

    names = (*assets, "PROVENANCE.json")
    lines = []
    for name in sorted(names):
        digest = hashlib.sha256((args.directory / name).read_bytes()).hexdigest()
        lines.append(f"{digest}  {name}\n")
    manifest = args.directory / "SHA256SUMS"
    manifest.write_text("".join(lines), encoding="ascii")
    subprocess.run(
        [openssl, "pkeyutl", "-sign", "-inkey", str(args.signing_key), "-rawin", "-in", str(manifest), "-out", str(args.directory / "SHA256SUMS.sig")],
        check=True,
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"release manifest: {error}", file=sys.stderr)
        raise SystemExit(1)
