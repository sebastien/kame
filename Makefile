GO ?= go
GOCACHE ?= /tmp/kame-go-cache
export GOCACHE

.PHONY: all build check test clean
all: build
build: version
	mkdir -p build dist
	cd src/go/kame && CGO_ENABLED=0 $(GO) build -ldflags="-X main.artifactMode=debug" -o ../../../build/kame.debug ./cmd/kame
	cd src/go/kame && CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w -X main.artifactMode=release' -o ../../../dist/kame ./cmd/kame
check test: version
	cd src/go/kame && CGO_ENABLED=0 $(GO) test ./...
clean:
	$(GO) clean -C src/go/kame

.PHONY: wasm
wasm:
	GO="$(GO)" bash src/sh/build-wasm.sh

.PHONY: version
version:
	bash src/sh/generate-version.sh

.PHONY: dist-wasm wasm-check
ifeq ($(KAME_TEST_PREBUILT),$(CURDIR))
dist-wasm:
	@test -f dist/kame.wasm -a -f dist/kame.js
else
dist-wasm: wasm
	python3 src/sh/stage-wasm.py
endif
wasm-check: wasm
	node tests/wasm/go-reactor.mjs

.PHONY: build/kame.debug dist/kame
build/kame.debug dist/kame: build

.PHONY: test-unit test-examples dist-native
test-unit: check
test-examples:
	cd examples/engine && CGO_ENABLED=0 $(GO) test ./...
dist-native: build
	python3 src/sh/package-native.py

.PHONY: release-assets dist-windows dist-release package-manifests
release-assets dist-windows: version dist-wasm
	python3 src/sh/build-release-assets.py
dist/kame-linux-x86_64 dist/kame-linux-arm64 dist/kame-darwin-x86_64 dist/kame-darwin-arm64 dist/kame-windows-x64.zip: release-assets

dist-release: release-assets
	cp VERSION dist/VERSION
	python3 src/sh/release_manifest.py --directory dist/release --source-directory dist --version "$$(cat VERSION)" --revision "$${KAME_RELEASE_REVISION:-$$(git rev-parse HEAD)}" --public-key "$(KAME_RELEASE_PUBLIC_KEY)" --signing-key "$(KAME_RELEASE_SIGNING_KEY)"
package-manifests: release-assets
	python3 src/sh/release_package_manifests.py --directory dist --output-directory etc --version "$$(cat VERSION)"

.PHONY: release
release:
	bash src/sh/release.sh $(if $(repo),--repo "$(repo)") $(if $(notes),--notes-file "$(notes)")
