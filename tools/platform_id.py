#!/usr/bin/env python3
"""Print the stable OS/architecture key used for native release artifacts."""

import platform
import sys


SYSTEMS = {
    "Linux": "linux",
    "Darwin": "darwin",
    "FreeBSD": "freebsd",
    "NetBSD": "netbsd",
    "OpenBSD": "openbsd",
}
MACHINES = {
    "x86_64": "x86_64",
    "amd64": "x86_64",
    "aarch64": "arm64",
    "arm64": "arm64",
}


system = SYSTEMS.get(platform.system())
machine = MACHINES.get(platform.machine().lower())
if not system or not machine:
    print(
        f"unsupported native release platform: {platform.system()} {platform.machine()}",
        file=sys.stderr,
    )
    raise SystemExit(1)

print(f"{system}-{machine}")
