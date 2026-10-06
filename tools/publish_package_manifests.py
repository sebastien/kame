#!/usr/bin/env python3
"""Validate release package definitions and stage them in Homebrew/Scoop repos."""

import argparse
import hashlib
import json
import pathlib
import re
import shutil
import sys
import tempfile


RELEASE_BASE = "https://github.com/sebastien/kame/releases/download/v{}"


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def repository_path(root, *parts):
    destination = root
    for part in parts[:-1]:
        destination = destination / part
        if destination.is_symlink():
            raise ValueError(f"refusing symlinked repository directory: {destination}")
        if destination.exists() and not destination.is_dir():
            raise ValueError(f"repository path is not a directory: {destination}")
        destination.mkdir(exist_ok=True)
    return destination / parts[-1]


def write_atomically(destination, content):
    if destination.is_symlink():
        raise ValueError(f"refusing to replace symlink: {destination}")
    with tempfile.NamedTemporaryFile(
        mode="wb", dir=destination.parent, prefix=f".{destination.name}.", delete=False
    ) as temporary:
        temporary.write(content)
        temporary_path = pathlib.Path(temporary.name)
    temporary_path.replace(destination)


def require_repository(path, label):
    if not path.is_dir() or not (path / ".git").exists():
        raise ValueError(f"{label} is not a checked out git repository: {path}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--release-directory", required=True, type=pathlib.Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--homebrew-tap", required=True, type=pathlib.Path)
    parser.add_argument("--scoop-bucket", required=True, type=pathlib.Path)
    args = parser.parse_args()

    version = args.version.removeprefix("v")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?", version):
        parser.error(f"invalid release version: {args.version}")

    release = args.release_directory
    formula_path = release / "kame.rb"
    scoop_path = release / "kame.json"
    cli_path = release / "kame.com"
    windows_path = release / "kame-windows-x64.exe"
    for path in (formula_path, scoop_path, cli_path, windows_path):
        if path.is_symlink() or not path.is_file():
            parser.error(f"missing or unsafe release asset: {path.name}")

    try:
        formula = formula_path.read_text(encoding="utf-8")
        scoop_bytes = scoop_path.read_bytes()
        expected_base = RELEASE_BASE.format(version)
        if f'url "{expected_base}/kame.com"' not in formula:
            raise ValueError("Homebrew formula URL does not match the selected release")
        if f'version "{version}"' not in formula:
            raise ValueError("Homebrew formula version does not match the selected release")
        if f'sha256 "{sha256(cli_path)}"' not in formula:
            raise ValueError("Homebrew formula digest does not match kame.com")

        scoop = json.loads(scoop_bytes)
        if not isinstance(scoop, dict):
            raise ValueError("Scoop manifest must be a JSON object")
        if scoop.get("version") != version:
            raise ValueError("Scoop manifest version does not match the selected release")
        if scoop.get("url") != f"{expected_base}/kame-windows-x64.exe":
            raise ValueError("Scoop manifest URL does not match the selected release")
        if scoop.get("hash") != sha256(windows_path):
            raise ValueError("Scoop manifest digest does not match the Windows CLI")
        if scoop.get("bin") != [["kame-windows-x64.exe", "kame"]]:
            raise ValueError("Scoop manifest does not expose the native Windows CLI as kame")

        require_repository(args.homebrew_tap, "Homebrew tap")
        require_repository(args.scoop_bucket, "Scoop bucket")
        formula_target = repository_path(args.homebrew_tap, "Formula", "kame.rb")
        scoop_target = repository_path(args.scoop_bucket, "bucket", "kame.json")
        write_atomically(formula_target, formula_path.read_bytes())
        write_atomically(scoop_target, scoop_bytes)
    except json.JSONDecodeError as error:
        print(f"package manifest publication: invalid Scoop manifest: {error}", file=sys.stderr)
        return 1
    except (OSError, ValueError) as error:
        print(f"package manifest publication: {error}", file=sys.stderr)
        return 1

    print(f"staged Homebrew and Scoop manifests for kame {version}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
