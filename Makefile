.PHONY: test test-sanitize demo

test:
	so test ./...

test-sanitize:
	CC=clang so test -check=sanitize -panic=abort ./...

demo:
	so run ./examples/demo
