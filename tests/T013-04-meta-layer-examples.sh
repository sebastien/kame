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
cp -R "$CLI_ROOT/examples/composition/publication" ./composition-kash
cp -R "$CLI_ROOT/examples/kash" ./kash
# A user may have run the lesson already. Remove only copied generated outputs.
rm -rf ./rules/publication/public ./rules/publication/.kame
rm -rf ./composition/publication/public ./composition/publication/.kame
rm -rf ./composition-kash/public ./composition-kash/.kame
rm -f ./kash/report.txt
test-ok "isolated example copies"

test-step "standalone Kash process lessons execute from isolated copies"
(
	cd kash
	cli_run -- do run ./01-values.kash
	cli_expect_status 0
	cli_expect_stdout $'Little publication\n[hello world.txt]\n[second.txt]\n[HELLO WORLD.TXT]\n[SECOND.TXT]\nHELLO\n<>\n<two words>\n<a; echo surprise>\nsource=./sample.txt\n'
	cli_run -- do run ./02-pipelines.kash
	cli_expect_status 0
	cli_expect_stdout $'hello\nworld\nhello\nworld\n12\nhello\nworld\n'
	cli_expect_file ./report.txt $'hello\nworld\nreport complete\n'
	cli_run -- do run ./03-recovery.kash
	cli_expect_status 0
	cli_expect_stdout $'continued after an accepted non-match\nno needle in the sample\nno result\npreview only\nsample validated\nnote=intro\n'
)

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
		cli_run -- do fmt -n "$file"
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
		printf 'edited hello\n' >./notes/hello.txt
		cli_run --
		cli_expect_status 0
		cli_expect_stdout_contains 'rendered ./public/hello.txt' 'assembled ./public/index.txt'
		not_rendered ./public/world.txt
		cli_expect_file ./public/hello.txt $'LITTLE PUBLICATION\nEDITED HELLO\n'
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

	test-step "$project: reject a note that collides with the index"
	(
		cd "$project"
		printf 'reserved note\n' >./notes/index.txt
		cli_run -- do plan ./public/index.txt
		cli_expect_status 1
		cli_expect_stderr_contains USER_ERROR 'notes/index.txt is reserved'
		cli_run --
		cli_expect_status 1
		cli_expect_stderr_contains USER_ERROR 'notes/index.txt is reserved'
		rm ./notes/index.txt
		cli_run --
		cli_expect_status 0
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

test-step "composed Kash lesson invokes Kame subprocesses from PATH"
(
	cd composition-kash
	mkdir -p ./bin
	ln -s "$CLI_ROOT/build/kame.debug" ./bin/kame
	PATH="$PWD/bin:$PATH"
	export PATH
	cli_run -- do run ./publish.kash
	cli_expect_status 0
	cli_expect_stdout_contains 'Publication files: 3' 'COMPUTATIONS COMPOSE' 'HELLO FROM KAME'
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

test-step "legacy language projects build from isolated copies"
cp -R "$CLI_ROOT/examples/language" ./language
for project in hello patterns; do
	(
		cd "language/$project"
		rm -rf ./build ./.kame
		cli_run -- do fmt -n ./*.kmk
		cli_expect_status 0
		cli_run --
		cli_expect_status 0
		if [ "$project" = hello ]; then
			cli_expect_file ./build/greeting.txt $'Hello from Kame!\n'
		else
			for name in alpha beta; do
				if cmp -s "./src/$name.txt" "./build/$name.txt"; then test-ok "$name copied"; else test-fail "$name output differs"; fi
			done
		fi
	)
done

test-step "blog tutorial builds, discovers posts and rebuilds the index"
if command -v pandoc >/dev/null 2>&1; then
	cp -R "$CLI_ROOT/examples/tutorial-blog" ./blog
	(
		cd blog
		rm -rf ./build ./.kame
		cli_run -- do fmt -n ./Makefile.kmk
		cli_expect_status 0
		cli_run --
		cli_expect_status 0
		for file in index.html author.html posts/hello-world.html projects/this-site.html style.css tools/row.sh; do
			if [ -s "./build/$file" ]; then test-ok "blog produced $file"; else test-fail "blog missing $file"; fi
		done
		cp -p ./build/index.html ./index-before
		cp -p ./build/posts/hello-world.html ./post-before
		cli_run --
		cli_expect_status 0
		if [ ! ./build/index.html -nt ./index-before ] && [ ! ./build/posts/hello-world.html -nt ./post-before ]; then
			test-ok "unchanged blog outputs retain their timestamps"
		else
			test-fail "unchanged blog rebuilt outputs"
		fi
		printf '\nBody-only edit.\n' >>./content/posts/hello-world.md
		cli_run --
		cli_expect_status 0
		if [ ./build/index.html -nt ./index-before ] && [ ./build/posts/hello-world.html -nt ./post-before ] && cmp -s ./index-before ./build/index.html; then
			test-ok "body edit rebuilds both page and index without changing index contents"
		else
			test-fail "body edit did not match the documented rebuild behavior"
		fi
		printf '%s\n' '---' 'title: "Incremental rebuilds"' 'date: "2026-09-27"' 'description: "A new post."' '---' 'New post body.' >./content/posts/incremental-rebuilds.md
		cli_run --
		cli_expect_status 0
		if [ -s ./build/posts/incremental-rebuilds.html ] && grep '<li>' ./build/index.html | head -1 | grep -Fq 'incremental-rebuilds.html'; then
			test-ok "new post is built and listed first"
		else
			test-fail "blog did not discover or order the new post"
		fi
		cli_run -- clean
		cli_expect_status 0
		if [ ! -e ./build ]; then test-ok "blog clean removes generated files"; else test-fail "blog clean left build outputs"; fi
	)
else
	test_log_message "SKIP blog execution: optional pandoc dependency is not installed"
fi

test-end
