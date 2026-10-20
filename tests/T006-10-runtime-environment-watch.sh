#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — warm watch dependency restoration
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-10 warm watch execution observations"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=(env _=kame-environment-test SHLVL=1 "$CLI_BIN"); else runner=(env _=kame-environment-test SHLVL=1 node "$CLI_ROOT/dist/kame.js"); fi
	for scenario in equal discovered task definition; do
		test-step "$backend warm watch restores $scenario observations"
		project="$TMPDIR/$backend-$scenario"
		mkdir -p "$project"
		cat >"$project/Makefile.kmk" <<'KMK'
SHELL = /bin/sh
CONTENT = (text (read ./definition-input))
./constant : ./input
	printf fixed > @>; printf x >> child-runs
./downstream : ./constant
	cp @< @>; printf x >> parent-runs
./discovered : ./input ; [shell: kash]
	(yield! (text (read ./extra)))
task read-task : ./input ; [shell: kash]
	printf %s (text (read ./extra)) > task-output
	printf x >> task-runs
task definition-task : ./input ; [shell: kash]
	printf %s $CONTENT > definition-output
	printf x >> definition-runs
KMK
		printf A >"$project/input"
		printf A >"$project/extra"
		printf one >"$project/definition-input"
		case "$scenario" in
		equal) read -r target input before after output expected <<<'./downstream input D F child-runs xxx' ;;
		discovered) read -r target input before after output expected <<<'./discovered extra B C discovered C' ;;
		task) read -r target input before after output expected <<<'read-task extra D E task-output E' ;;
		definition) read -r target input before after output expected <<<'definition-task definition-input two three definition-output three' ;;
		esac
		# Establish a persisted warm record, then change and settle it before watching.
		for value in first first changed changed; do
			if [ "$value" = changed ]; then printf '%s' "$before" >"$project/$input"; fi
			"${runner[@]}" -C "$project" "$target" >"$project/out" 2>"$project/err"
		done
		"${runner[@]}" --output json --watch -C "$project" "$target" >"$project/events" 2>"$project/watch-err" &
		watch_pid=$!
		ready=0
		for _attempt in {1..100}; do
			if jq -e -s --arg target "$target" 'any(.[]; .type == "target-completed" and .target == $target) and any(.[]; .type == "watch-idle" and .cycle == 1 and .status == "success")' "$project/events" >/dev/null 2>&1; then
				ready=1
				break
			fi
			sleep 0.05
		done
		cp -p "$project/$input" "$project/stamp"
		printf '%s' "$after" >"$project/$input"
		touch -r "$project/stamp" "$project/$input"
		for _attempt in {1..100}; do
			if [ "$(cat "$project/$output")" = "$expected" ] && jq -e -s 'any(.[]; .type == "watch-idle" and .cycle >= 2)' "$project/events" >/dev/null 2>&1; then break; fi
			sleep 0.05
		done
		kill -TERM "$watch_pid" 2>/dev/null || true
		wait "$watch_pid" 2>/dev/null || true
		valid=1
		case "$scenario" in
		equal)
			if [ "$(cat "$project/parent-runs")" != x ] || ! jq -e -s '([.[] | select(.type == "target-started" and .target == "./downstream")] | length == 1) and ([.[] | select(.type == "target-completed" and .target == "./constant")] | length >= 2)' "$project/events" >/dev/null; then valid=0; fi
			;;
		task) [ "$(cat "$project/task-runs")" = xxx ] || valid=0 ;;
		definition) [ "$(cat "$project/definition-runs")" = xxx ] || valid=0 ;;
		esac
		if [ "$ready" = 1 ] && [ "$valid" = 1 ] && [ "$(cat "$project/$output")" = "$expected" ]; then test-ok "$backend $scenario watch restoration"; else
			test-fail "$backend $scenario watch restoration"
			cat "$project/events" "$project/watch-err"
		fi
	done
done
test-end
