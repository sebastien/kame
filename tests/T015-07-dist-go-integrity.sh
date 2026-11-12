#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — Go release artifact integrity
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T015-07 Go release compiler identity"
cd "$CLI_ROOT"
make release-assets >/dev/null
for system in linux darwin; do
	for pair in x86_64:amd64 arm64:arm64; do
		label=${pair%:*}; arch=${pair#*:}
		test-step "$system $label is a standalone Go binary"
		metadata=$(go version -m "dist/kame-$system-$label")
		if grep -q 'CGO_ENABLED=0' <<<"$metadata" && grep -q "GOOS=$system" <<<"$metadata" && grep -q "GOARCH=$arch" <<<"$metadata" && ! grep -q 'solod.dev' <<<"$metadata"; then
			test-ok "Go build metadata matches the release platform"
		else
			test-fail "unexpected build metadata: $metadata"
		fi
	done
done
test-step "the Windows launcher bundle carries an adjacent ESM WASM runtime"
if python3 - <<'PY'
import json, zipfile
with zipfile.ZipFile('dist/kame-windows-x64.zip') as archive:
    assert set(archive.namelist()) == {'kame.exe', 'kame.js', 'kame.wasm', 'package.json', 'VERSION'}
    assert archive.read('kame.exe').startswith(b'MZ')
    assert archive.read('kame.wasm').startswith(b'\0asm')
    assert json.loads(archive.read('package.json'))['type'] == 'module'
PY
then test-ok "launcher, reactor and ESM metadata are bundled together"; else test-fail "invalid Windows bundle"; fi
test-end
