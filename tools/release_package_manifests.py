#!/usr/bin/env python3
"""Generate release-pinned Homebrew and Scoop installation manifests."""

import argparse
import hashlib
import json
import pathlib


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", required=True, type=pathlib.Path)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    root = args.directory
    version = args.version
    base = f"https://github.com/sebastien/kame/releases/download/v{version}"

    formula = f'''class Kame < Formula
  desc "Build tool for projects described with Kame"
  homepage "https://github.com/sebastien/kame"
  url "{base}/kame.com"
  version "{version}"
  sha256 "{sha256(root / "kame.com")}"

  def install
    bin.install "kame.com" => "kame"
  end

  test do
    assert_match version.to_s, shell_output("#{{bin}}/kame --version")
  end
end
'''
    # Release assets have one flat asset name. Keep the formula at the release
    # root so its download URL is stable; a tap publisher can copy it into its
    # own Formula/kame.rb path.
    (root / "kame.rb").write_text(formula, encoding="utf-8")

    scoop = {
        "version": version,
        "description": "Build tool for projects described with Kame",
        "homepage": "https://github.com/sebastien/kame",
        "url": f"{base}/kame-windows-x64.exe",
        "hash": sha256(root / "kame-windows-x64.exe"),
        "bin": [["kame-windows-x64.exe", "kame"]],
    }
    (root / "kame.json").write_text(
        json.dumps(scoop, sort_keys=True, indent=2) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
