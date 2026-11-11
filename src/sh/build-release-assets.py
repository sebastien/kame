#!/usr/bin/env python3
"""Build the standalone Go binaries and the Windows Node/WASM launcher bundle."""
import os
from pathlib import Path
import shutil
import subprocess
import zipfile

root = Path(__file__).resolve().parents[2]
dist = root / "dist"
dist.mkdir(exist_ok=True)
environment = {**os.environ, "CGO_ENABLED": "0"}
for system in ("linux", "darwin"):
    for arch, label in (("amd64", "x86_64"), ("arm64", "arm64")):
        name = f"kame-{system}-{label}"
        subprocess.run([
            "go", "build", "-trimpath", "-ldflags=-s -w -X main.artifactMode=release",
            "-o", str(dist / name), "./cmd/kame",
        ], cwd=root / "src/go/kame", env={**environment, "GOOS": system, "GOARCH": arch}, check=True)
        print(name, flush=True)

windows = dist / "windows"
windows.mkdir(exist_ok=True)
subprocess.run([
    "go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(windows / "kame.exe"), "./cmd/kame-launcher",
], cwd=root / "src/go/kame", env={**environment, "GOOS": "windows", "GOARCH": "amd64"}, check=True)
for name in ("kame.js", "kame.wasm"):
    shutil.copy2(dist / name, windows / name)
(windows / "package.json").write_text('{"type":"module"}\n')
with zipfile.ZipFile(dist / "kame-windows-x64.zip", "w", compression=zipfile.ZIP_DEFLATED) as archive:
    for name in ("kame.exe", "kame.js", "kame.wasm", "package.json"):
        archive.write(windows / name, name)
print("kame-windows-x64.zip", flush=True)
