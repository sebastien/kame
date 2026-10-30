#!/usr/bin/env bash
# Specs: 013 runnable examples; 017 Kash; 042 streaming; 046 publication.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T013-05 representative Kash workflows"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null

for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	cp -R "$CLI_ROOT/examples/kash/." "$work/"
	invoke() { timeout 30 "${runner[@]}" do run -C "$work" "$@"; }
	check() {
		local file="$1" expected="$2" status=0
		shift 2
		invoke "$work/$file" "$@" >"$work/out" 2>"$work/err" || status=$?
		printf '%s' "$expected" >"$work/expected"
		if [ "$status" = 0 ] && cmp -s "$work/out" "$work/expected"; then
			test-ok "$backend $file output"
		else
			test-fail "$backend $file (status $status)"
			cat "$work/out" "$work/err"
		fi
		if compgen -G "$work/kash-*.*" >/dev/null; then test-fail "$backend leaked workspace"; else test-ok "$backend private workspace cleaned"; fi
	}
	test-step "$backend parse and canonical formatting"
	for file in "$work"/*.kash; do
		if "${runner[@]}" do parse --lang kash "$file" >"$work/parse" 2>"$work/err" \
			&& "${runner[@]}" do fmt --lang kash -n "$file" >"$work/fmt" 2>>"$work/err"; then
			test-ok "$backend $(basename "$file") parses and is formatted"
		else
			test-fail "$backend $(basename "$file") parse/format"
			cat "$work/err"
		fi
	done
	test-step "$backend workflows"
	check 01-values.kash $'Little publication\n[hello world.txt]\n[second.txt]\n[HELLO WORLD.TXT]\n[SECOND.TXT]\nHELLO\n<>\n<two words>\n<a; echo surprise>\nsource=./sample.txt\n'
	check 02-pipelines.kash $'hello\nworld\nhello\nworld\n12\nhello\nworld\n'
	cli_expect_file "$work/report.txt" $'hello\nworld\nreport complete\n'
	check 03-recovery.kash $'continued after an accepted non-match\nno needle in the sample\nno result\npreview only\nsample validated\nnote=intro\n'
	check 04-operations.kash $'typed argv\nfirst\nsecond\nrecovered\nfallback\ncaptured=payload\n'
	printf 'progress\n' >"$work/expected-err"
	if cmp -s "$work/err" "$work/expected-err"; then test-ok "$backend progress stays on stderr"; else test-fail "$backend output routing"; fi
	check 05-batch.kash $'checked: first note\nchecked: second note\nprocessed queued item\n'
	check 05-batch.kash $'custom label: first note\ncustom label: second note\nprocessed queued item\n' -- --prefix "custom label"
	check 06-environment.kash $'checked\ntwo words\npreview\nrecord=preview\n'
	check 07-concurrency.kash $'checks complete: 0\nbackground report\n'
	check 08-streaming.kash $'errors:\nERROR failed\nnormal:\nINFO start\nINFO done\n'
	check 09-workers.kash $'ALPHA\nBETA\nGAMMA\nDELTA\n'
	check 10-publication.kash $'Publication: Notes "preview"\n'

	test-step "$backend interpreter argument separator"
	status=0
	if [ "$backend" = native ]; then
		mkdir -p "$work/bin"
		ln -s "$CLI_BIN" "$work/bin/kame"
		(cd "$work" && PATH="$work/bin:$PATH" timeout 30 ./05-batch.kash -- --prefix "custom label") >"$work/out" 2>"$work/err" || status=$?
	else
		"${runner[@]}" do kash -C "$work" "$work/05-batch.kash" -- --prefix "custom label" >"$work/out" 2>"$work/err" || status=$?
	fi
	printf 'custom label: first note\ncustom label: second note\nprocessed queued item\n' >"$work/expected"
	if [ "$status" = 0 ] && cmp -s "$work/out" "$work/expected"; then test-ok "$backend interpreter forwards script options"; else test-fail "$backend interpreter arguments (status $status)"; cat "$work/err"; fi

	test-step "$backend failure cleanup and no-clobber publication"
	printf 'unmatched\n' >"$work/sample.txt"
	status=0
	invoke "$work/07-concurrency.kash" >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" != 0 ] && ! compgen -G "$work/kash-concurrency.*" >/dev/null; then test-ok "$backend failed async check cleans workspace"; else test-fail "$backend failure cleanup"; fi
	printf 'original' >"$work/published"
	printf 'replacement' >"$work/staged"
	status=0
	invoke --lang kash -c '(publish! ./staged ./published)' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" != 0 ] && grep -q FS_EXISTS "$work/err" && [ "$(cat "$work/published")" = original ] && [ "$(cat "$work/staged")" = replacement ]; then test-ok "$backend refuses clobber without consuming staging"; else test-fail "$backend publication collision"; fi
done
test-end
