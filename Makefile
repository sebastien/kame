KAME_DIR = src/go/kame
EXAMPLES_DIR = examples
# wasi-sdk is provisioned project-locally by Mise. Its LLVM bundle supplies
# wasm-ld; unlike the asdf plugin shims, that binary lives beneath wasi-sdk/bin.
WASM_SDK ?= $(shell mise where asdf:mise-plugins/mise-wasi-sdk 2>/dev/null)/wasi-sdk
WASM_CC ?= $(WASM_SDK)/bin/clang
WASM_LD ?= $(WASM_SDK)/bin/wasm-ld
WASM_INITIAL_MEMORY ?= 16777216
WASM_MAX_MEMORY ?= 67108864


.PHONY: build dist wasm wasm-translate test-wasm build/kame.debug build/kame.sanitize dist/kame \
	test test-so test-examples test-go test-cli test-all test-sanitize test-leaks fmt clean demo

build: dist

test: test-so test-examples test-go test-cli

test-so:
	cd $(KAME_DIR) && so test ./...

test-examples:
	cd $(EXAMPLES_DIR) && so test ./...

test-go:
	cd $(KAME_DIR) && go test ./cmd/kame

test-cli: build/kame.debug
	tests/harness.sh

test-wasm: wasm
	tests/T010-01-wasm-loader.sh

test-all: test test-leaks

test-sanitize:
	cd $(KAME_DIR) && CC=clang so test -check=sanitize -panic=abort ./...
	cd $(EXAMPLES_DIR) && CC=clang so test -check=sanitize -panic=abort ./...

test-leaks: build/kame.sanitize
	cd $(KAME_DIR) && CC=clang go test -asan ./cmd/kame
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

# The freestanding target begins with ABI primitives. It stays outside the
# default build until the portable runtime no longer reaches hosted imports.
# WASM_LD is explicit because a host clang installation need not provide it.
wasm-translate:
	mkdir -p build/wasm/c
	cd $(KAME_DIR) && so translate -o ../../../build/wasm/c ./host/wasm

wasm: wasm-translate
	command -v $(WASM_CC)
	command -v $(WASM_LD)
	$(WASM_CC) --target=wasm32-unknown-unknown -ffreestanding -nostdlib -DSO_HEAP_SIZE=8388608 -fuse-ld=$(WASM_LD) -I build/wasm/c -I $(KAME_DIR)/host/wasm -Wl,--no-entry -Wl,--export-memory -Wl,--initial-memory=$(WASM_INITIAL_MEMORY) -Wl,--max-memory=$(WASM_MAX_MEMORY) -Wl,--export=kame_wasm_abi_version -Wl,--export=kame_wasm_alloc -Wl,--export=kame_wasm_free -Wl,--export=kame_wasm_event_header -Wl,--export=kame_wasm_copy -Wl,--export=kame_wasm_eval_pure -Wl,--export=kame_wasm_eval_source_pure -Wl,--export=kame_wasm_instance_create -Wl,--export=kame_wasm_instance_free -Wl,--export=kame_wasm_source_compile -Wl,--export=kame_wasm_expression_request -Wl,--export=kame_wasm_expression_begin -Wl,--export=kame_wasm_expression_cancel -Wl,--export=kame_wasm_step -Wl,--export=kame_wasm_next_event_header -Wl,--export=kame_wasm_next_request_kind -Wl,--export=kame_wasm_next_request_data_length -Wl,--export=kame_wasm_request_data_copy -Wl,--export=kame_wasm_event_payload_copy -Wl,--export=kame_wasm_event_discard -Wl,--export=kame_wasm_complete_bytes -Wl,--export=kame_wasm_complete_text -Wl,--export=kame_wasm_complete_nil -Wl,--export=kame_wasm_complete_failure -Wl,--export=kame_wasm_result_copy -Wl,--export=kame_wasm_diagnostic_length -Wl,--export=kame_wasm_diagnostic_copy $$(find build/wasm/c -name '*.c' -print) tools/wasm/kame_wasm_abi.c -o build/wasm/kame.wasm

build/kame.debug:
	mkdir -p build
	cd $(KAME_DIR) && so build -check=warn -o ../../../build/kame.debug ./cmd/kame

build/kame.sanitize:
	mkdir -p build
	cd $(KAME_DIR) && CC=clang so build -check=sanitize -panic=abort -o ../../../build/kame.sanitize ./cmd/kame

dist/kame:
	mkdir -p dist
	cd $(KAME_DIR) && CFLAGS=-O3 so build -assert=off -panic=exit -o ../../../dist/kame ./cmd/kame
