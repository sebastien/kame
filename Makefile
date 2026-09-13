.PHONY: test-sanitize

test-sanitize:
	CC=clang so test -check=sanitize -panic=abort ./core
