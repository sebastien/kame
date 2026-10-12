#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — wildcard entry kinds and watch
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-02 wildcard kind masks"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/assets/nested" "$work/outside"
	printf file >"$work/assets/file"
	printf nested >"$work/assets/nested/sub"
	printf excluded >"$work/outside/secret"
	printf flag-shaped >"$work/+files"
	ln -s file "$work/assets/linkfile"
	ln -s ../outside "$work/assets/dirlink"
	ln -s missing "$work/assets/dangling"
	ln -s loop "$work/assets/loop"
	ln -s missing "$work/assets/nested/dangling"
	mkfifo "$work/assets/fifo"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local want="$1" expression="$2"; shift 2
		if timeout 20 "${runner[@]}" do run -C "$work" --lang expr --allow-read="$work" "$@" -c "$expression" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$want" ]; then
			test-ok "$backend $expression"
		else
			test-fail "$backend wildcard kind result: $expression"
			cat "$work/out" "$work/err"
		fi
	}
	reject() {
		local code="$1" expression="$2" status=0
		timeout 20 "${runner[@]}" do run -C "$work" --lang expr --allow-read="$work" -c "$expression" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend $expression ($status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend union masks, flag order, duplicates and computed flags"
	check '["./assets/file"]' '(wildcard :+files ./assets/*)'
	check '["./assets/nested"]' '(wildcard ./assets/* :+dir)'
	check '["./assets/dangling" "./assets/dirlink" "./assets/linkfile" "./assets/loop"]' '(wildcard :+symlinks ./assets/*)'
	check '["./assets/dangling" "./assets/dirlink" "./assets/file" "./assets/linkfile" "./assets/loop"]' '(wildcard :+files ./assets/* :+symlinks :-dir :+files)'
	check '["./assets/file" "./assets/nested"]' '(wildcard :-symlinks ./assets/* :+files :+dir)'
	check '["./assets/fifo"]' '(wildcard :-files :-dir :-symlinks ./assets/*)'
	check '["./assets/file"]' '(let [flags ["+files" "-symlinks"]] (wildcard flags.0 ./assets/* flags.1))'
	check '["./+files"]' '(wildcard :+files ./+files)'
	test-step "$backend traversal, literal matches, dangling links and empty results"
	check '["./assets/file" "./assets/nested/sub"]' '(wildcard :+files ./assets/** ./assets/nested/*)'
	check '["./assets/nested"]' '(wildcard :+dir ./assets/**)'
	check '["./assets/dangling"]' '(wildcard :+symlinks ./assets/dangling)'
	check '["./assets/nested/dangling"]' '(wildcard :+symlinks ./assets/*/dangling)'
	check '[]' '(wildcard :+files ./absent/**)'
	check '["./assets/dirlink/secret"]' '(wildcard :+files ./assets/dirlink/*)'
	check ':true' '(get (lstat ./assets/file) "regular")'
	check ':false' '(get (lstat ./assets/linkfile) "regular")'
	test-step "$backend invalid flags and real metadata errors"
	for expression in '(wildcard :+files)' '(wildcard :-dir :-symlinks)' '(wildcard :+files :-files ./assets/*)' '(wildcard :-symlinks :+symlinks ./assets/*)' '(wildcard "+unknown" ./assets/*)' '(wildcard "-unknown" ./assets/*)' '(wildcard "+files/child")'; do reject EXPR_INVALID "$expression"; done
	reject FS_ERR '(wildcard :+files ./assets/loop/*)'
	test-step "$backend memory resources expose normalized entry kinds"
	check '["mem://masks/root"]' '(wildcard :+dir (resource "mem://masks/*"))' --allow-read=mem://masks/ --allow-write=mem://masks/ -c '(write! (resource "mem://masks/root/input") "x")'
	check '["mem://masks/root/input"]' '(wildcard :+files (resource "mem://masks/**"))' --allow-read=mem://masks/ --allow-write=mem://masks/ -c '(write! (resource "mem://masks/root/input") "x")'
	test-step "$backend kind changes invalidate shared filtered watch consumers"
	mkdir -p "$work/watch/items/slot"
	printf x >"$work/watch/seed"
	cat >"$work/watch/Makefile.kmk" <<'KMK'
SHELL = kash
FILES = (wildcard :+files ./items/*)
DIRS = (wildcard :+dir ./items/*)
report :
	(write! ./file-report (join FILES ","))
	(write! ./dir-report (join DIRS ","))
KMK
	settled() {
		local files="$1" dirs="$2" cycles="$3"
		for attempt in {1..300}; do
			if [ -f "$work/watch/file-report" ] && [ -f "$work/watch/dir-report" ] && [ "$(cat "$work/watch/file-report")" = "$files" ] && [ "$(cat "$work/watch/dir-report")" = "$dirs" ] && [ "$(grep -c '"type":"watch-idle"' "$work/watch/log" || true)" -ge "$cycles" ]; then return 0; fi
			sleep .025
		done
		return 1
	}
	timeout 30 "${runner[@]}" -C "$work/watch" --json --watch report >"$work/watch/log" 2>"$work/watch/err" &
	watcher=$!
	if settled '' './items/slot' 1; then test-ok "$backend initial shared directory selection"; else test-fail "$backend initial filtered watch"; fi
	rmdir "$work/watch/items/slot"
	cp -p "$work/watch/seed" "$work/watch/items/slot"
	if settled './items/slot' '' 2; then test-ok "$backend unchanged membership changes directory to file"; else test-fail "$backend directory-to-file invalidation"; fi
	rm "$work/watch/items/slot"
	ln -s ../seed "$work/watch/items/slot"
	if settled '' '' 3; then test-ok "$backend same name, bytes and followed metadata change file to symlink"; else test-fail "$backend file-to-link invalidation"; fi
	if [ -s "$work/watch/err" ]; then test-fail "$backend filtered watch diagnostic"; cat "$work/watch/err"; fi
	kill -TERM "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
done
test-end
