#!/usr/bin/env python3
"""Stage the host's Go binary under its canonical release platform name."""
from pathlib import Path
import shutil
import subprocess

root = Path(__file__).resolve().parents[2]
platform = subprocess.check_output(
    ["python3", str(root / "src/sh/platform_id.py")], text=True
).strip()
destination = root / "dist/native" / platform / "kame"
destination.parent.mkdir(parents=True, exist_ok=True)
shutil.copy2(root / "dist/kame", destination)
destination.chmod(0o755)
print(destination.relative_to(root))
