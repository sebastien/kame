#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — Redirection, capture, grants, dependencies and effects
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T017-04 embedded Kash redirection parity"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/redirections"
mkdir -p "$work"

check() {
	local expression="$1" expected_status="$2" expected_stdout="$3" code="${4:-}"
	shift 4
	set +e
	"${command[@]}" do expr --allow-run "$@" -c "$expression" >"$work/stdout" 2>"$work/stderr"
	local status=$?
	set -e
	if [ "$status" = "$expected_status" ] && [ "$(cat "$work/stdout")" = "$expected_stdout" ]; then test-ok "$backend: $expression"; else test-fail "$backend: status=$status: $expression"; fi
	if [ -n "$code" ]; then
		if grep -q "$code" "$work/stderr"; then test-ok "$backend: $code"; else test-fail "$backend: missing $code"; fi
	fi
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	printf '%s' input >"$work/input"
	output="$work/output with spaces"
	printf '%s' old >"$output"
	grants=("--allow-read=$work" "--allow-write=$work")

	test-step "$backend: file streams, truncate, append and empty capture"
	check "\$(cat < '$work/input')" 1 '' PARSE_ERR "${grants[@]}"
	check "\$(cat < \"$work/input\")" 0 '"input"' '' "${grants[@]}"
	check "\$(printf first > \"$output\")" 0 '""' '' "${grants[@]}"
	if [ "$(cat "$output")" = first ]; then test-ok "$backend truncates output"; else test-fail "$backend output bytes differ"; fi
	check "\$(printf second >> \"$output\")" 0 '""' '' "${grants[@]}"
	if [ "$(cat "$output")" = firstsecond ]; then test-ok "$backend appends output"; else test-fail "$backend append differs"; fi
	check "\$(< \"$work/input\" cat | tr a-z A-Z > \"$output\")" 0 '""' '' "${grants[@]}"
	if [ "$(cat "$output")" = INPUT ]; then test-ok "$backend redirects pipeline endpoints"; else test-fail "$backend pipeline output differs"; fi
	check "(let [name \"$output\"] \$(printf typed > \$name))" 0 '""' '' "${grants[@]}"
	check "(cat \$(printf a >> \"$output\") \$(printf b >> \"$output\"))" 0 '""' '' "${grants[@]}"
	if [ "$(cat "$output")" = typedab ]; then test-ok "$backend redirects exactly once across suspension"; else test-fail "$backend replayed append"; fi
	check "(count [\$(printf x > \"$output\") (out \"after\")])" 0 'after2' '' "${grants[@]}"
	check '$(printf "%s" "a>b" "c<d")' 0 '"a>bc<d"' '' "${grants[@]}"

	test-step "$backend: redirected binary data is not captured or decoded"
	check "\$(printf \"\\\\377\" > \"$output\")" 0 '""' '' "${grants[@]}"
	check "\$(cat < \"$output\" > \"$work/copy\")" 0 '""' '' "${grants[@]}"
	if cmp -s "$output" "$work/copy"; then test-ok "$backend preserves binary redirected bytes"; else test-fail "$backend changed redirected bytes"; fi
	check "\$(cat < \"$output\")" 1 '' CAPTURE_ENCODING "${grants[@]}"

	test-step "$backend: validation and capability denial never truncate files"
	printf '%s' sentinel >"$output"
	check "\$(printf denied > \"$output\")" 1 '' CAP_DENIED "--allow-read=$work"
	check "\$(cat < \"$work/input\" > \"$output\")" 1 '' CAP_DENIED "--allow-write=$work"
	check "\$(printf invalid @([bad: 1]) > \"$output\")" 1 '' EXPR_INVALID "${grants[@]}"
	check "\$(printf invalid > \"$output\" | cat)" 1 '' PARSE_ERR "${grants[@]}"
	check "\$(cat < \"$work/missing\" > \"$output\")" 1 '' HOST_FAIL "${grants[@]}"
	check "\$(printf invalid > @([\"$output\"]))" 1 '' EXPR_INVALID "${grants[@]}"
	check '$(printf invalid > "")' 1 '' EXPR_INVALID "${grants[@]}"
	check "\$(printf denied > \"$output\")" 1 '' CAP_DENIED "--allow-write=$work/other"
	set +e
	"${command[@]}" do expr --allow-run=/usr/bin/printf "${grants[@]}" -c "\$(/usr/bin/printf denied | /usr/bin/cat > \"$output\")" >"$work/stdout" 2>"$work/stderr"
	status=$?
	set -e
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/stderr"; then test-ok "$backend authorizes every stage before truncation"; else test-fail "$backend did not deny the later stage"; fi
	if [ "$(cat "$output")" = sentinel ]; then test-ok "$backend leaves output untouched on invalid or denied requests"; else test-fail "$backend truncated a denied output"; fi

	test-step "$backend: relative paths use the invocation directory"
	set +e
	"${command[@]}" do expr -C "$work" --allow-run --allow-read=./ --allow-write=./ -c '$(cat < input > relative)' >"$work/stdout" 2>"$work/stderr"
	status=$?
	set -e
	if [ "$status" = 0 ] && [ "$(cat "$work/relative")" = input ] && [ "$(cat "$work/stdout")" = '""' ]; then test-ok "$backend resolves relative redirections"; else test-fail "$backend relative redirection failed"; fi
done

test-step "redirection AST and formatting match across backends"
printf '%s\n' '$(< ${input} cat | cat >> @(output))' >"$work/expression.km"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" --lang expr "$work/expression.km" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" --lang expr "$work/expression.km" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation redirection parity"; else test-fail "$operation redirection parity"; fi
done

test-end
