#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — Slice B framing and routing
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-02 Kash framing and conditional routing parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/framing"
mkdir -p "$work"
node -e 'const fs = require("node:fs"); for (const c of ["a", "b"]) fs.writeFileSync(process.argv[1] + "/" + c, (c.repeat(60000) + "\n").repeat(30)); fs.writeFileSync(process.argv[1] + "/utf8", "é".repeat(32767) + "x\n");' "$work"

run_case() {
	local text="$1" expected="$2" code="${3:-}" language="${4:-kash}"
	local status=0
	timeout 20 "${command[@]}" 'do' run --lang "$language" --allow-run --allow-read --allow-write --capture-limit=128 -c "$text" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = "$expected" ]; then test-ok "$backend status: $text"; else test-fail "$backend expected $expected got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "$code" ]; then
		if grep -q "$code" "$work/$backend.err"; then test-ok "$backend: $code"; else test-fail "$backend missing $code: $(cat "$work/$backend.err")"; fi
	fi
}
expect() {
	local message="$1"
	shift
	if "$@"; then test-ok "$backend: $message"; else test-fail "$backend: $message"; fi
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	selected="$work/$backend-selected"
	remainder="$work/$backend-remainder"
	test-step "$backend: framing preserves delimiters and partial EOF"
	run_case '$(printf "a\r\nb\nlast" |(frame :lines) |(encode :lines) | cat)' 0 '' expr
	expect "raw payload round trip" grep -Fq '"a\r\nb\nlast"' "$work/$backend.out"
	run_case '$(printf "a\nb" |(frame :lines))' 0 '' expr
	expect "terminal adapter drains before capture completes" grep -Fq '"a\nb"' "$work/$backend.out"
	run_case "\$(printf \"a\nb\n\" |(frame :lines) |(filter (eq item \"a\") | cat > $selected))" 0 '' expr
	expect "terminal filter keeps an independent remainder" grep -Fq '"b\n"' "$work/$backend.out"
	run_case "printf \"a\r\nb\nlast\" |(frame :lines) |(filter (eq item \"a\") | cat > $selected) | cat > $remainder" 0
	printf 'a\r\n' >"$work/want"
	expect "predicate strips CRLF, raw selected bytes do not" cmp -s "$selected" "$work/want"
	printf 'b\nlast' >"$work/want"
	expect "selected items leave the remainder" cmp -s "$remainder" "$work/want"
	run_case '$(printf "a\nb\n" |(frame :lines) |(filter (eq item "a") | tr a z |(frame :lines) >&) | cat)' 0 '' expr
	expect "next continuation joins transformed and unmatched items" grep -Fq 'z\n' "$work/$backend.out"

	test-step "$backend: next and otherwise are lexical and distinct"
	for exit in '>&' '>@'; do
		run_case "printf \"a\nb\n\" |(frame :lines) |(filter (eq item \"a\") | tr a z |(frame :lines) $exit) |(filter (eq item \"z\") | cat > $selected) | cat > $remainder" 0
		if [ "$exit" = '>&' ]; then
			expect "next rejoin is screened by the next filter" test "$(cat "$selected")" = z
			expect "next remainder" test "$(cat "$remainder")" = b
		else
			expect "otherwise bypasses the next filter" test ! -s "$selected"
			expect "otherwise reaches ordinary continuation" grep -qx z "$remainder"
		fi
	done
	run_case "printf \"a\nb\nc\n\" |(frame :lines) |(filter (ne item \"c\") |(filter (eq item \"a\") | tr a z |(frame :lines) >@) | cat > $selected) | cat > $remainder" 0
	expect "nested otherwise stays inside selected graph" grep -qx z "$selected"
	expect "nested decomposition preserves outer remainder" test "$(cat "$remainder")" = c

	test-step "$backend: static failures precede launch and truncation"
	marker="$work/$backend-marker"
	printf preserve >"$selected"
	run_case "sh -c \"touch $marker\" |(filter :true | cat > $selected) | cat" 1 KASH_FRAME_REQUIRED
	expect "invalid framing launches no source" test ! -e "$marker"
	expect "invalid framing preserves destinations" test "$(cat "$selected")" = preserve
	run_case 'printf a |(frame :lines) |(filter :true | cat >&) | cat' 1 KASH_FRAME_MISMATCH
	run_case 'printf a |(frame :lines) |(filter :true | cat |(frame :lines) >@)' 1 KASH_REJOIN_OUT_OF_SCOPE
	run_case 'printf a >&' 1 KASH_REJOIN_OUT_OF_SCOPE
	run_case '$(printf a |(frame :lines) |(filter :true | cat) | cat)' 1 KASH_AMBIGUOUS_CAPTURE expr

	test-step "$backend: predicate failures, EOF, bounded records and ownership"
	run_case 'printf "a\n" |(frame :lines) |(filter item | cat) | cat' 1 KASH_PREDICATE_BOOLEAN
	run_case 'printf "a\n" |(frame :lines) |(filter (read ./missing) | cat) | cat' 1 KASH_PREDICATE_EFFECT
	run_case 'printf "a\n" |(frame :lines) |(filter $(printf x) | cat) | cat' 1 KASH_PREDICATE_EFFECT
	run_case 'printf "\\377\n" |(frame :lines) | cat' 1 KASH_FRAME_ENCODING
	run_case 'head -c 65537 /dev/zero |(frame :lines) | cat' 1 KASH_RECORD_LIMIT
	run_case "printf \"\" |(frame :lines) |(filter :true | cat > $selected) | cat > $remainder" 0
	expect "empty input closes both branches" test ! -s "$remainder"
	run_case "printf \"a\nb\n\" |(frame :lines) |(filter :true | cat > $selected) | cat > $remainder &" 0
	expect "async graph joins its selected branch" test "$(cat "$selected")" = $'a\nb'
	run_case 'printf "a\n" |(frame :lines) |(filter item | cat) | cat ? printf recovered' 0
	expect "graph recovery includes predicate failures" grep -qx recovered "$work/$backend.out"

	test-step "$backend: frame-safe merge and bounded streaming exceed capture limits"
	run_case '$((seq 1 10000 |(frame :lines), seq 1 10000 |(frame :lines)) |(merge) |(filter :false | cat > /dev/null) | wc -l)' 0 '' expr
	expect "merge preserves 20000 records" grep -Fq '"20000\n"' "$work/$backend.out"
	run_case "seq 1 10000 |(frame :lines) |(broadcast) (cat > $selected, sh -c \"sleep 0.1; cat\" > $remainder)" 0
	expect "slow framed broadcast is lossless" cmp -s "$selected" "$remainder"
	run_case "\$((cat < $work/a |(frame :lines), cat < $work/b |(frame :lines)) |(merge) |(filter (eq (count item) 60000) | wc -l > $selected) | wc -l)" 0 '' expr
	expect "long records larger than a pipe remain atomic" test "$(tr -d '[:space:]' <"$selected")" = 60
	expect "every long record passed its predicate" grep -Fq '"0\n"' "$work/$backend.out"
	run_case "cat < $work/utf8 |(frame :lines) |(filter (eq (count item) 32768) | cat > $selected) | cat > $remainder" 0
	expect "UTF-8 splits and exact 64 KiB record limit" cmp -s "$selected" "$work/utf8"
	expect "UTF-8 predicate sees decoded characters" test ! -s "$remainder"

	test-step "$backend: predicate configuration participates in build reuse"
	build="$work/$backend-build"
	mkdir -p "$build"
	printf 'seed\n' >"$build/seed"
	cat >"$build/Makefile.kmk" <<'KMK'
SHELL = kash
needle = "a"
./result : ./seed
	sh -c "echo run >> runs; echo a; echo b" |(frame :lines) |(filter (eq item needle) | cat > @>) | cat
KMK
	for needle in a a b; do
		if timeout 20 "${command[@]}" -C "$build" -f ./Makefile.kmk --set "needle=\"$needle\"" ./result >"$work/$backend.out" 2>"$work/$backend.err"; then test-ok "$backend predicate build"; else test-fail "$backend predicate build: $(cat "$work/$backend.err")"; fi
		expect "predicate configuration selects current records" test "$(cat "$build/result")" = "$needle"
	done
	expect "unchanged predicates reuse; changed definitions invalidate" test "$(wc -l <"$build/runs")" -eq 2
done
test-end
