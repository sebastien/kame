KAME_DIR = src/go/kame
EXAMPLES_DIR = examples
# wasi-sdk is provisioned project-locally by Mise. Its LLVM bundle supplies
# wasm-ld; unlike the asdf plugin shims, that binary lives beneath wasi-sdk/bin.
WASM_SDK ?= $(shell mise where asdf:mise-plugins/mise-wasi-sdk 2>/dev/null)/wasi-sdk
WASM_CC ?= $(WASM_SDK)/bin/clang
WASM_LD ?= $(WASM_SDK)/bin/wasm-ld
WASM_INITIAL_MEMORY ?= 16777216
WASM_MAX_MEMORY ?= 67108864


.PHONY: build dist dist-ape dist-release dist-wasm wasm wasm-check wasm-portable wasm-translate test-wasm version-source build/kame.debug build/kame.sanitize dist/kame dist/kame.com \
	test test-so test-examples test-go test-cli test-all test-sanitize test-leaks fmt clean demo

build: dist

test: test-so test-examples test-go test-cli

test-so: $(KAME_DIR)/cmd/kame/version_generated.go
	cd $(KAME_DIR) && so test ./...

test-examples:
	cd $(EXAMPLES_DIR) && so test ./...

test-go: $(KAME_DIR)/cmd/kame/version_generated.go
	cd $(KAME_DIR) && go test ./cmd/kame

test-cli: build/kame.debug
	tests/harness.sh

wasm-portable:
	tools/wasm/check-portable.sh

wasm-check: wasm wasm-portable
	node tools/wasm/check-imports.mjs build/wasm/kame.wasm

test-wasm: wasm-check
	tests/T010-01-wasm-loader.sh
	tests/T010-02-wasm-abi.sh
	tests/T010-03-wasm-target.sh
	tests/T010-04-wasm-forwarding.sh
	tests/T010-05-wasm-rule-target.sh
	tests/T010-06-wasm-cli.sh
	tests/T010-07-wasm-cat.sh
	tests/T010-08-wasm-glob.sh
	tests/T010-09-wasm-parse.sh
	tests/T010-10-wasm-fmt.sh
	tests/T010-11-wasm-plan.sh
	tests/T010-12-wasm-graph.sh
	tests/T010-13-wasm-tools.sh
	tests/T010-14-wasm-json.sh
	tests/T010-15-wasm-process-control.sh
	tests/T010-16-wasm-cancellation.sh
	tests/T010-17-wasm-human-output.sh
	tests/T010-18-wasm-clock.sh
	tests/T010-19-wasm-cache.sh
	tests/T010-20-wasm-build-includes.sh
	tests/T007-09-lib-wildcard-union.sh
	tests/T007-10-lib-record-lookup.sh
	tests/T007-11-lib-tools.sh
	tests/T009-13-cli-configuration.sh
	tests/T004-09-lang-rule-header-composition.sh
	tests/T004-10-lang-line-continuations.sh
	tests/T004-11-lang-wildcard-inputs.sh
	tests/T004-12-lang-optional-includes.sh
	tests/T010-21-wasm-build-effects.sh
	tests/T014-02-patterns-ergonomics.sh
	tests/T016-01-cli-render.sh
	tests/T017-02-wasm-capture.sh
	tests/T017-03-eval-pipelines.sh
	tests/T017-04-eval-redirections.sh
	tests/T017-05-eval-setup.sh
	tests/T017-06-kash-source.sh
	tests/T017-07-cli-run-session.sh
	tests/T017-08-cli-run-includes.sh
	tests/T017-09-cli-run-json.sh
	tests/T017-10-cli-run-dry-run.sh
	tests/T017-11-kash-accept-exit.sh
	tests/T017-12-kash-command-recovery.sh
	tests/T017-13-kash-value-recovery.sh
	tests/T017-14-kash-if.sh
	tests/T017-15-kash-match.sh
	tests/T017-16-kash-environment.sh
	tests/T017-17-eval-process-expressions.sh
	tests/T017-18-kash-async.sh

test-all: test test-leaks

test-sanitize: $(KAME_DIR)/cmd/kame/version_generated.go
	cd $(KAME_DIR) && CC=clang so test -check=sanitize -panic=abort ./...
	cd $(EXAMPLES_DIR) && CC=clang so test -check=sanitize -panic=abort ./...

test-leaks: build/kame.sanitize
	cd $(KAME_DIR) && GOGC=off CC=clang go test -asan ./cmd/kame
	cd $(KAME_DIR) && CC=clang so test -check=sanitize -panic=abort ./...
	cd $(EXAMPLES_DIR) && CC=clang so test -check=sanitize -panic=abort ./...
	ASAN_OPTIONS=detect_leaks=1:halt_on_error=1 UBSAN_OPTIONS=halt_on_error=1 CLI_BIN="$(CURDIR)/build/kame.sanitize" tests/harness.sh $$(find tests -type f -name 'T*.sh' ! -name 'T013-03-meta-binary.sh')

fmt: build/kame.debug
	git ls-files -z -- '*.go' | xargs -0r gofmt -w
	git ls-files -z -- '*.sh' | xargs -0r shfmt -w
	./build/kame.debug do fmt -i Makefile.kmk

clean:
	rm -rf build dist

demo:
	cd $(EXAMPLES_DIR) && so run ./demo

dist: build/kame.debug dist/kame

# WebAssembly release artifacts. Kept out of the default dist so a host without
# the wasi-sdk toolchain can still build the native CLI.
dist-wasm: dist/kame.wasm dist/kame.js

dist/kame.wasm: wasm
	mkdir -p dist
	cp build/wasm/kame.wasm dist/kame.wasm

dist/kame.js: src/js/kame.js
	mkdir -p dist
	cp src/js/kame.js dist/kame.js

# Stage the flat release assets with a stamped launcher and SHA256SUMS. Publishing
# is manual and out of band.
dist-release: dist/kame.com dist/kame.wasm dist/kame.js
	mkdir -p dist/release/bin
	cp dist/kame.com dist/release/kame.com
	cp dist/kame.wasm dist/release/kame.wasm
	cp dist/kame.js dist/release/kame.js
	sed "s|^KAME_STAMP=.*|KAME_STAMP=\"$$(cat VERSION)\"|" bin/kame > dist/release/bin/kame
	chmod +x dist/release/bin/kame
	cd dist/release && { sha256sum kame.com kame.wasm kame.js bin/kame 2>/dev/null || shasum -a 256 kame.com kame.wasm kame.js bin/kame; } | sort -k2 > SHA256SUMS

dist-ape: dist/kame.com

# The freestanding target begins with ABI primitives. It stays outside the
# default build until the portable runtime no longer reaches hosted imports.
# WASM_LD is explicit because a host clang installation need not provide it.
# Every file the module links. A file target keeps repeated test builds cheap:
# relink only when a portable source changes.
WASM_SOURCES := $(shell find $(KAME_DIR)/cli $(KAME_DIR)/core $(KAME_DIR)/diagnostic $(KAME_DIR)/host $(KAME_DIR)/lang $(KAME_DIR)/operations $(KAME_DIR)/program tools/wasm -type f \( -name '*.go' -o -name '*.c' -o -name '*.h' \))

wasm-translate:
	mkdir -p build/wasm/c
	cd $(KAME_DIR) && so translate -o ../../../build/wasm/c ./host/wasm

wasm: build/wasm/kame.wasm

build/wasm/kame.wasm: $(WASM_SOURCES)
	command -v $(WASM_CC)
	command -v $(WASM_LD)
	mkdir -p build/wasm/c
	cd $(KAME_DIR) && so translate -o ../../../build/wasm/c ./host/wasm
	$(WASM_CC) --target=wasm32-unknown-unknown -ffreestanding -nostdlib -DSO_HEAP_SIZE=8388608 -fuse-ld=$(WASM_LD) -I build/wasm/c -I $(KAME_DIR)/host/wasm -Wl,--no-entry -Wl,--export-memory -Wl,--initial-memory=$(WASM_INITIAL_MEMORY) -Wl,--max-memory=$(WASM_MAX_MEMORY) -Wl,--export=kame_wasm_abi_version -Wl,--export=kame_wasm_alloc -Wl,--export=kame_wasm_free -Wl,--export=kame_wasm_event_header -Wl,--export=kame_wasm_copy -Wl,--export=kame_wasm_cli -Wl,--export=kame_wasm_eval_pure -Wl,--export=kame_wasm_parse -Wl,--export=kame_wasm_format -Wl,--export=kame_wasm_eval_source_pure -Wl,--export=kame_wasm_instance_create -Wl,--export=kame_wasm_instance_free -Wl,--export=kame_wasm_source_compile -Wl,--export=kame_wasm_set_source_name -Wl,--export=kame_wasm_expression_request -Wl,--export=kame_wasm_set_forwarding -Wl,--export=kame_wasm_set_directory -Wl,--export=kame_wasm_host_set_file -Wl,--export=kame_wasm_host_set_env -Wl,--export=kame_wasm_target_begin -Wl,--export=kame_wasm_prepare -Wl,--export=kame_wasm_plan -Wl,--export=kame_wasm_graph -Wl,--export=kame_wasm_tools -Wl,--export=kame_wasm_set_tool_path -Wl,--export=kame_wasm_tools_check -Wl,--export=kame_wasm_inspection_grant -Wl,--export=kame_wasm_target_event -Wl,--export=kame_wasm_process_started -Wl,--export=kame_wasm_process_stream -Wl,--export=kame_wasm_process_terminal -Wl,--export=kame_wasm_expression_begin -Wl,--export=kame_wasm_expression_cancel -Wl,--export=kame_wasm_expression_effect_kind -Wl,--export=kame_wasm_expression_effect_length -Wl,--export=kame_wasm_expression_effect_copy -Wl,--export=kame_wasm_step -Wl,--export=kame_wasm_next_event_header -Wl,--export=kame_wasm_next_request_kind -Wl,--export=kame_wasm_next_request_data_length -Wl,--export=kame_wasm_next_request_key_length -Wl,--export=kame_wasm_request_key_copy -Wl,--export=kame_wasm_next_request_record_length -Wl,--export=kame_wasm_request_record_copy -Wl,--export=kame_wasm_request_data_copy -Wl,--export=kame_wasm_event_payload_copy -Wl,--export=kame_wasm_event_discard -Wl,--export=kame_wasm_complete_bytes -Wl,--export=kame_wasm_complete_text -Wl,--export=kame_wasm_complete_nil -Wl,--export=kame_wasm_complete_json -Wl,--export=kame_wasm_complete_failure -Wl,--export=kame_wasm_result_copy -Wl,--export=kame_wasm_result_kind -Wl,--export=kame_wasm_diagnostic_length -Wl,--export=kame_wasm_diagnostic_copy -Wl,--export=kame_wasm_instance_diagnostic_length -Wl,--export=kame_wasm_instance_diagnostic_copy -Wl,--export=kame_wasm_instance_diagnostic_span $$(find build/wasm/c -name '*.c' -print) tools/wasm/kame_wasm_abi.c -o build/wasm/kame.wasm

$(KAME_DIR)/cmd/kame/version_generated.go: VERSION tools/generate-version.sh
	tools/generate-version.sh

version-source: $(KAME_DIR)/cmd/kame/version_generated.go

KAME_SOURCES := $(shell find $(KAME_DIR) -type f -name '*.go')
KAME_BUILD_INPUTS := $(KAME_DIR)/cmd/kame/version_generated.go $(KAME_SOURCES) $(KAME_DIR)/go.mod $(KAME_DIR)/go.sum Makefile

build/kame.debug: KAME_BUILD_MODE=debug
build/kame.debug: $(KAME_BUILD_INPUTS)
	mkdir -p build
	cd $(KAME_DIR) && so build -check=warn -o ../../../build/kame.debug ./cmd/kame

build/kame.sanitize: KAME_BUILD_MODE=sanitize
build/kame.sanitize: $(KAME_BUILD_INPUTS)
	mkdir -p build
	cd $(KAME_DIR) && CC=clang so build -check=sanitize -panic=abort -o ../../../build/kame.sanitize ./cmd/kame

dist/kame: KAME_BUILD_MODE=release
dist/kame: $(KAME_BUILD_INPUTS)
	mkdir -p dist
	cd $(KAME_DIR) && CFLAGS=-O3 so build -assert=off -panic=exit -o ../../../dist/kame ./cmd/kame

build/tools/cosmocc/bin/cosmocc:
	tools/provision-cosmocc.sh build/tools/cosmocc

dist/kame.com: KAME_BUILD_MODE=release
dist/kame.com: build/tools/cosmocc/bin/cosmocc $(KAME_BUILD_INPUTS)
	mkdir -p dist
	cd $(KAME_DIR) && CC=$(CURDIR)/build/tools/cosmocc/bin/cosmocc CFLAGS=-O3 so build -assert=off -panic=exit -o ../../../dist/kame.com ./cmd/kame
