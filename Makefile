.PHONY: test test-cli test-all test-sanitize demo dist

test:
	so test ./...

test-cli:
	tests/harness.sh

test-all: test test-cli

test-sanitize:
	CC=clang so test -check=sanitize -panic=abort ./...

demo:
	so run ./examples/demo

dist: dist/littlemake.debug dist/littlemake

dist/littlemake.debug:
	mkdir -p dist
	so build -check=warn -o dist/littlemake.debug ./cmd/littlemake

dist/littlemake:
	mkdir -p dist
	CFLAGS=-O3 so build -assert=off -panic=exit -o dist/littlemake ./cmd/littlemake
