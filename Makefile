.PHONY: build dist dist/littlemake.debug dist/littlemake \
	test test-so test-go test-cli test-all test-sanitize fmt clean demo

build: dist

test: test-so test-go test-cli

test-so:
	so test ./...

test-go:
	go test ./cmd/littlemake

test-cli: dist/littlemake.debug
	tests/harness.sh

test-all: test test-sanitize

test-sanitize:
	CC=clang so test -check=sanitize -panic=abort ./...

fmt:
	git ls-files -z -- '*.go' | xargs -0r gofmt -w
	git ls-files -z -- '*.sh' | xargs -0r shfmt -w

clean:
	rm -rf dist generated

demo:
	so run ./examples/demo

dist: dist/littlemake.debug dist/littlemake

dist/littlemake.debug:
	mkdir -p dist
	so build -check=warn -o dist/littlemake.debug ./cmd/littlemake

dist/littlemake:
	mkdir -p dist
	CFLAGS=-O3 so build -assert=off -panic=exit -o dist/littlemake ./cmd/littlemake
