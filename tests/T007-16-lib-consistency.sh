#!/usr/bin/env bash
# Specs: docs/spec/{004-language,007-library,017-kash}.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-16 predicate, sequence and Kash value consistency"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { timeout 30 "${runner[@]}" do run -C "$work" "$@"; }
	if [ "$(timeout 30 "${runner[@]}" do kash -c '(out! "alias")')" = alias ]; then test-ok "$backend Kash runner alias"; else test-fail "$backend Kash runner alias"; fi
	check() {
		local want="$1" text="$2" status=0
		invoke -l expr -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$want" ]; then test-ok "$backend $text"; else test-fail "$backend $text"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend predicate names, aliases, presence and emptiness"
	for op in eq? eq is = ==; do check ':true' "($op [1] [1.0])"; done
	for op in ne? ne '!='; do check ':true' "($op 1 2)"; done
	for op in in? has?; do check ':true' "($op [x: :nil] [[x: :nil]])"; done
	check '[:true :true :true :true :true :true :true :true :true :true]' '[(nil? :nil) (bool? :false) (int? 1) (float? 1.5) (number? 1) (number? 1.5) (callable? uppercase) (resource? (resource "mem://test/file")) (pattern? ./{name:*}) (not (nil? uppercase))]'
	check '[:true :true :true :false :false :false :false :false]' '[(empty? "") (empty? []) (empty? (merge)) (empty? :nil) (empty? 0) (empty? :false) (empty? " ") (empty? uppercase)]'
	check '[:true :false :nil]' '[(has-key? "x" [x: :nil]) (has-key? "y" [x: :nil]) (if (has-key? "x" [x: :nil]) (get "x" [x: :nil]) (fail! "unselected default"))]'
	printf '\0\377' >"$work/bytes"
	invoke --allow-read -l kash -c '(out! (json [(bytes? (read ./bytes)) (empty? (read ./bytes))]))' >"$work/out"
	if [ "$(cat "$work/out")" = '[true,false]' ]; then test-ok "$backend binary kind/emptiness"; else test-fail "$backend binary predicates"; fi

	test-step "$backend exact ordering and consistent sequence calls"
	check '[:true :true :true :true :true :true]' '[(lt? 9007199254740992 9007199254740993) (gt? 9007199254740993 9007199254740992.0) (< -9223372036854775808 9223372036854775807) (< 9223372036854775807 9223372036854775808.0) (> 9223372036854775807 9223372036854774784.0) (< -1 -0.5)]'
	check '[1 [1 2] "HELLO" 2 "zero"]' '[(apply ([x] x) [1]) (apply ([xs...] xs) [1 2]) (apply uppercase ["hello"]) (apply count [[1 2]]) (apply ([] "zero") [])]'
	check '["é" :nil :nil]' '[(first "é🙂") (first "") (first [])]'
	for text in '(apply uppercase [])' '(apply uppercase ["a" "b"])' '(in? 1 [x: 1])' '(success? [status: 0])' '(success? [status: "0" signal: 0])'; do
		status=0
		invoke -l expr -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err"; then test-ok "$backend rejects $text"; else test-fail "$backend accepted $text"; cat "$work/out" "$work/err"; fi
	done

	test-step "$backend leading path atoms and explicit function status tests"
	cat >"$work/values.kash" <<'KASH'
root = "./private"
(base) = root
path = @{root}/config.json; computed = @(base)/config.json
(if (eq? path computed) :nil (fail! "path boundary"))
defn probe status
	return status ?
if (success? (probe 75))
	(fail! "accepted failure was truthy")
else
	(out! "retry\n")
if (success? (apply probe [0]))
	(out! "success\n")
defn collect values...
	return (count values) ?
result = (apply collect [1 2 3])
(out! "arity=" result.status "\n" path "\n")
KASH
	invoke -l kash "$work/values.kash" >"$work/out" 2>"$work/err"
	printf 'retry\nsuccess\narity=3\n./private/config.json\n' >"$work/expected"
	if cmp -s "$work/out" "$work/expected" && [ ! -s "$work/err" ]; then test-ok "$backend values/status execution"; else test-fail "$backend values/status contract"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do fmt -l kash "$work/values.kash" >"$work/formatted.kash"
	"${runner[@]}" do fmt -l kash "$work/formatted.kash" >"$work/again.kash"
	invoke -l kash "$work/formatted.kash" >"$work/formatted-out"
	if cmp -s "$work/formatted.kash" "$work/again.kash" && cmp -s "$work/formatted-out" "$work/expected"; then test-ok "$backend value paths round trip"; else test-fail "$backend value path formatting"; fi
	status=0
	invoke -l kash -c $'defn fatal\n\treturn 75\nif (success? (fatal))\n\t(out! "unreachable")\n' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 75 ] && [ ! -s "$work/out" ]; then test-ok "$backend success? cannot accept a failure"; else test-fail "$backend status predicate swallowed failure"; fi
done
test-end
