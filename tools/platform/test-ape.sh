#!/bin/sh
# Spec: docs/spec/015-distribution.md — APE host conformance
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: test-ape.sh PATH_TO_KAME_COM" >&2
	exit 2
fi

ape_directory=$(CDPATH= cd "$(dirname "$1")" && pwd -P)
ape_name=$(basename "$1")
ape_path=$ape_directory/$ape_name
work_path=$(mktemp -d "${TMPDIR:-/tmp}/kame-ape.XXXXXX")
trap 'rm -rf "$work_path"' 0 HUP INT TERM

version=$(sh "$ape_path" --version)
case "$version" in
	"kame "*) ;;
	*)
		echo "APE version output is invalid: $version" >&2
		exit 1
		;;
esac

cat >"$work_path/Makefile.kmk" <<'EOF'
task platform-smoke :
	printf 'platform-smoke-ok\n'
	printf '%s\n' "$KAME_PLATFORM_ENV" > platform-env.txt

task platform-timeout :
	sleep 2
	printf 'late\n' > timeout-marker
EOF

cd "$work_path"
output=$(sh "$ape_path" --env KAME_PLATFORM_ENV=passed -f Makefile.kmk platform-smoke)
if [ "$output" != "platform-smoke-ok" ]; then
	echo "APE task output mismatch: $output" >&2
	exit 1
fi
if [ "$(cat platform-env.txt)" != passed ]; then
	echo "APE task did not preserve the host process environment" >&2
	exit 1
fi

set +e
sh "$ape_path" --timeout 100 -f Makefile.kmk platform-timeout \
	>"$work_path/timeout.out" 2>"$work_path/timeout.err"
timeout_status=$?
set -e
if [ "$timeout_status" -eq 0 ] || [ -e timeout-marker ]; then
	echo "APE timeout did not stop and reap its recipe process" >&2
	cat "$work_path/timeout.err" >&2
	exit 1
fi

printf 'APE host conformance passed: %s\n' "$version"
