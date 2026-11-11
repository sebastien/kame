#!/usr/bin/env python3
"""Atomically publish the WASM pair while other CLI processes may load it."""
from pathlib import Path
import os
import shutil
import tempfile

root = Path(__file__).resolve().parents[2]
destination = root / "dist"
destination.mkdir(exist_ok=True)
for source, name in ((root / "build/wasm/kame.wasm", "kame.wasm"), (root / "src/js/kame.js", "kame.js")):
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=destination, prefix=f".{name}.", delete=False) as output:
            temporary = Path(output.name)
            with source.open("rb") as input:
                shutil.copyfileobj(input, output)
            output.flush()
            os.fsync(output.fileno())
        temporary.chmod(0o644)
        os.replace(temporary, destination / name)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
