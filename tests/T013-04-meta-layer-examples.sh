#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — Runnable examples and fixture isolation
# Spec: docs/spec/004-language.md — Definitions, includes, expressions, rules
# Spec: docs/spec/017-kash.md — Embedded command capture and typed arguments
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T013-04 independent layers and composition examples"
test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "copy lesson inputs, never run in the source examples"
cp -R "$CLI_ROOT/examples/expressions" ./expressions
mkdir -p ./rules ./composition
cp -R "$CLI_ROOT/examples/rules/publication" ./rules/publication
cp -R "$CLI_ROOT/examples/composition/publication" ./composition/publication
# A user may have run the lesson already. Remove only copied generated outputs.
rm -rf ./rules/publication/public ./rules/publication/.kame
rm -rf ./composition/publication/public ./composition/publication/.kame
test-ok "isolated example copies"

test-step "value programs, functions, pipes, discovery and lazy failure"
(
	cd expressions
	cli_run -- -f ./01-values.km publication
	cli_expect_status 0
	cli_expect_stdout '[title: "Little publication" pages: ["./public/hello.txt" "./public/world.txt"] count: 2]'
	cli_run -- -f ./01-values.km first-page
	cli_expect_status 0
	cli_expect_stdout '"./public/hello.txt"'
	cli_run -- -f ./01-values.km unused
	cli_expect_status 1
	cli_expect_stderr_contains EXPR_INVALID
	cli_run -- -f ./01-values.km publication
	cli_expect_status 0
	cli_run -- -f ./02-functions.km pages
	cli_expect_status 0
	cli_expect_stdout '["./public/world.txt" "./public/hello.txt" "./public/hello.txt"]'
	cli_run -- -f ./02-functions.km publication
	cli_expect_status 0
	cli_expect_stdout '[pages: ["./public/hello.txt" "./public/world.txt"] labels: ["HELLO.TXT" "WORLD.TXT"]]'
	cli_run -- --allow-read -f ./03-resources.km publication
	cli_expect_status 0
	cli_expect_stdout '[sources: ["./notes/hello.txt" "./notes/world.txt"] pages: ["./public/hello.txt" "./public/world.txt"] count: 2]'
	printf 'a new note\n' >./notes/third.txt
	cli_run -- --allow-read -f ./03-resources.km publication
	cli_expect_status 0
	cli_expect_stdout_contains './notes/third.txt' './public/third.txt' 'count: 3'
	if [ ! -e ./public ]; then
		test-ok "calculating paths did not create artifacts"
	else
		test-fail "a value program created public/"
	fi
	for file in ./*.km; do
		cli_run -- do parse --lang script "$file"
		cli_expect_status 0
		# The formatter expands value pipes into nested applications.
		cli_run -- do fmt "$file"
		cli_expect_status 0
	done
)

not_rendered() {
	if grep -Fq "rendered $1" "$CLI_OUT"; then
		test-fail "unaffected page rendered: $1"
	else
		test-ok "unaffected page skipped: $1"
	fi
}

for project in rules/publication composition/publication; do
	test-step "$project: plan, demand, freshness and explicit action"
	(
		cd "$project"
		cli_run -- do fmt -n ./Makefile.kmk
		cli_expect_status 0
		cli_run -- do plan ./public/hello.txt
		cli_expect_status 0
		cli_run -- do inputs ./public/hello.txt
		cli_expect_status 0
		cli_expect_stdout_contains './notes/hello.txt' './banner.txt'
		cli_run -- -n ./public/hello.txt
		cli_expect_status 0
		if [ ! -e ./public ]; then
			test-ok "inspection did not produce files"
		else
			test-fail "inspection executed a recipe"
		fi
		cli_run -- ./public/hello.txt
		cli_expect_status 0
		cli_expect_file ./public/hello.txt $'LITTLE PUBLICATION\nHELLO FROM KAME\n'
		if [ ! -e ./public/world.txt ]; then
			test-ok "unrequested sibling was not produced"
		else
			test-fail "unrequested sibling was produced"
		fi
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_contains 'rendered ./public/world.txt' 'assembled ./public/index.txt'
		not_rendered ./public/hello.txt
		cli_expect_file ./public/index.txt $'LITTLE PUBLICATION\nHELLO FROM KAME\nLITTLE PUBLICATION\nCOMPUTATIONS COMPOSE\n'
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_empty
		cli_run -- check
		cli_expect_status 0
		cli_expect_stdout $'publication checked\n'
		cli_run -- check
		cli_expect_status 0
		cli_expect_stdout $'publication checked\n'
	)

	test-step "$project: selective changes, shared inputs and graph growth"
	(
		cd "$project"
		sleep 1
		printf 'edited hello\n' >./notes/hello.txt
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_contains 'rendered ./public/hello.txt' 'assembled ./public/index.txt'
		not_rendered ./public/world.txt
		cli_expect_file ./public/hello.txt $'LITTLE PUBLICATION\nEDITED HELLO\n'
		sleep 1
		printf 'LITTLE PUBLICATION, REVISED\n' >./banner.txt
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_contains 'rendered ./public/hello.txt' 'rendered ./public/world.txt' 'assembled ./public/index.txt'
		printf 'third note\n' >./notes/third.txt
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_contains 'rendered ./public/third.txt' 'assembled ./public/index.txt'
		not_rendered ./public/hello.txt
		not_rendered ./public/world.txt
		cli_expect_file ./public/third.txt $'LITTLE PUBLICATION, REVISED\nTHIRD NOTE\n'
		cli_expect_file ./public/index.txt $'LITTLE PUBLICATION, REVISED\nEDITED HELLO\nLITTLE PUBLICATION, REVISED\nTHIRD NOTE\nLITTLE PUBLICATION, REVISED\nCOMPUTATIONS COMPOSE\n'
		printf 'unrelated\n' >./unrelated.txt
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_empty
	)
done

test-step "native source composition reuses the value program unchanged"
(
	cd composition/publication
	cli_run -- do fmt -n ./publication.km
	cli_expect_status 0
	cli_run -- --allow-read -f ./publication.km publication
	cli_expect_status 0
	cli_expect_stdout_contains 'count: 3'
	cp "$CLI_OUT" ./value-result
	cli_run -- -f ./Makefile.kmk publication
	cli_expect_status 0
	if cmp -s ./value-result "$CLI_OUT"; then
		test-ok "standalone and included value programs agree"
	else
		test-fail "including the value program changed its result"
	fi
)

test-step "available Kash boundary: typed argv, capture, explicit grants"
cli_run -- do run --lang expr --allow-run -c '(let [names ["hello world.txt" "second.txt" "a; echo surprise"]] $(printf "[%s]\n" $names))'
cli_expect_status 0
cli_expect_stdout '"[hello world.txt]\n[second.txt]\n[a; echo surprise]\n"'
cli_run -- do run --lang expr --allow-run -c '(uppercase (strip $(printf "hello\n")))'
cli_expect_status 0
cli_expect_stdout '"HELLO"'
cli_run -- do run --lang expr -c '$(printf denied)'
cli_expect_status 1
cli_expect_stderr_contains CAP_DENIED

test-end
