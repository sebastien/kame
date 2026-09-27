KAME_DIR = src/go/kame
EXAMPLES_DIR = examples

.PHONY: build dist build/kame.debug build/kame.sanitize dist/kame \
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

test-all: test test-leaks

test-sanitize:
	cd $(KAME_DIR) && CC=clang so test -check=sanitize -panic=abort ./...
	cd $(EXAMPLES_DIR) && CC=clang so test -check=sanitize -panic=abort ./...

test-leaks: build/kame.sanitize
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

build/kame.debug:
	mkdir -p build
	cd $(KAME_DIR) && so build -check=warn -o ../../../build/kame.debug ./cmd/kame

build/kame.sanitize:
	mkdir -p build
	cd $(KAME_DIR) && CC=clang so build -check=sanitize -panic=abort -o ../../../build/kame.sanitize ./cmd/kame

dist/kame:
	mkdir -p dist
	cd $(KAME_DIR) && CFLAGS=-O3 so build -assert=off -panic=exit -o ../../../dist/kame ./cmd/kame
