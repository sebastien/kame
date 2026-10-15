#!/usr/bin/env bash
# Spec: docs/spec/005-evaluation.md — Truth and emptiness
# Spec: docs/spec/017-kash.md — Lossless capture and explicit conversion
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T005-06 truth, emptiness and lossless capture"
cd "$CLI_ROOT"
cli_build
make dist-wasm >/dev/null
work="$TMPDIR/truth-capture"
mkdir -p "$work"
: >"$work/empty"
printf x >"$work/nonempty"
cat >"$work/semantics.km" <<'KM'
(nonempty VALUE) = (> (count VALUE) 0)
(without-final-lf TEXT) = (if (ends? "\n" TEXT) (slice 0 -1 TEXT) TEXT)
EMPTY_RECORD = (parse-json "{}")
EMPTY_BYTES = (read ./empty)
VALUES = [:nil :false 0 0.0 "" [] EMPTY_RECORD EMPTY_BYTES]
truth = (map bool VALUES)
negation = (map not VALUES)
conditions = (map ([VALUE] (if VALUE :true :false)) VALUES)
filtered-count = (count (filter bool VALUES))
nonempty-values =
  (map nonempty ["" [] EMPTY_RECORD EMPTY_BYTES " " [:nil] [key: :nil] (read ./nonempty)])
unframed =
  (map without-final-lf ["" "x" "\n" "\n\n" "\r\n" "a\nb\n" " \té \t\n" "é\r" "x\n " "\r\n\r\n"])
KM
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		printf '%s' "$expected" >"$work/expected"
		if [ "$status" = 0 ] && cmp -s "$work/out" "$work/expected" && [ ! -s "$work/err" ]; then test-ok "$backend: $*"; else test-fail "$backend: $* (status $status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend nil/false-only truth and explicit emptiness"
	for entry in truth conditions; do
		check '[:false :false :true :true :true :true :true :true]' do run -C "$work" --allow-read=. semantics.km "$entry"
	done
	check '[:true :true :false :false :false :false :false :false]' do run -C "$work" --allow-read=. semantics.km negation
	check '6' do run -C "$work" --allow-read=. semantics.km filtered-count
	check '[:false :false :false :false :true :true :true :true]' do run -C "$work" --allow-read=. semantics.km nonempty-values
	check '""' do run --lang expr -c '(or "" (fail! "empty text became false"))'
	check '[]' do run --lang expr -c '(or [] (fail! "empty list became false"))'
	check '0' do run --lang expr -c '(and "" [] (parse-json "{}") 0)'
	check ':nil' do run --lang expr -c '(and :nil (fail! "nil did not short-circuit"))'
	check 'selected' do run --lang kash -c $'if (nop 0)\n\tprintf selected\nelse\n\tprintf forbidden'
	check '"selected"' do run --lang expr -c '(render (parse-json "{}") "plain" "@if(0)selected@else forbidden@end")'

	test-step "$backend exact stdout, typed capture reuse and opt-in conversion"
	check $' \té\nbody  \r\n\n' do run --allow-run -c '(out! $(printf " \té\nbody  \r\n\n"))'
	check $'\n\n' do run --allow-run -c '(out! $(printf "\n\n"))'
	check '' do run --allow-run -c '(out! $(printf ""))'
	check $'<a b\n\n>' do run --lang kash -c $'captured = $(printf "a b\\n\\n")\nprintf "<%s>" $captured'
	check '<>' do run --lang kash -c $'captured = $(printf "")\nprintf "<%s>" $captured'
	check '"release"' do run --allow-run --lang expr -c '(strip $(printf " release\n"))'
	check '["" "x" "" "\n" "\r" "a\nb" " \té \t" "é\r" "x\n " "\r\n\r"]' do run -C "$work" --allow-read=. semantics.km unframed
done
test-end
