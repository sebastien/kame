#!/bin/sh
# Spec: docs/spec/015-distribution.md — APE host conformance
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: test-ape.sh PATH_TO_KAME_COM PATH_TO_APE_SMOKE" >&2
	exit 2
fi

ape_directory=$(CDPATH= cd "$(dirname "$1")" && pwd -P)
ape_name=$(basename "$1")
ape_path=$ape_directory/$ape_name
smoke_directory=$(CDPATH= cd "$(dirname "$2")" && pwd -P)
smoke_name=$(basename "$2")
smoke_path=$smoke_directory/$smoke_name
work_path=$(mktemp -d "${TMPDIR:-/tmp}/kame-ape.XXXXXX")
trap 'rm -rf "$work_path"' 0 HUP INT TERM
cp "$ape_path" "$work_path/kame.com"
cp "$smoke_path" "$work_path/ape-smoke.com"
chmod +x "$work_path/kame.com"
chmod +x "$work_path/ape-smoke.com"
ape_path=$work_path/kame.com
smoke_path=$work_path/ape-smoke.com
ape_shell=${KAME_APE_SHELL:-bash}
ape_host=$(uname -s)
report_phase() {
	printf 'APE conformance phase: %s\n' "$1" >&2
}

# Assimilation selects the host's native executable format when available. It
# also avoids depending on shell-specific parsing of the binary polyglot.
if [ "$ape_host" = OpenBSD ]; then
	report_phase "prepare OpenBSD shell fallback"
	mkdir "$work_path/bin"
	ln -s "$(command -v gdd)" "$work_path/bin/dd"
	PATH=$work_path/bin:$PATH
	export PATH
else
	report_phase "assimilate APE"
	"$ape_shell" "$ape_path" --assimilate
	"$ape_shell" "$smoke_path" --assimilate
fi

run_binary() {
	binary=$1
	shift
	if [ "$ape_host" = OpenBSD ]; then
		"$ape_shell" "$binary" "$@"
	else
		"$binary" "$@"
	fi
}

report_phase "run minimal Cosmopolitan APE smoke program"
if run_binary "$smoke_path" >"$work_path/ape-smoke.out" 2>"$work_path/ape-smoke.err"; then
	smoke_status=0
	else
	smoke_status=$?
	fi
smoke_output=$(cat "$work_path/ape-smoke.out")
if [ "$smoke_status" -ne 0 ] || [ "$smoke_output" != "APE bootstrap smoke passed" ]; then
	echo "APE bootstrap smoke failed: exit=$smoke_status output=$smoke_output" >&2
	cat "$work_path/ape-smoke.err" >&2
	if [ "$ape_host" = OpenBSD ]; then
		report_phase "capture OpenBSD APE bootstrap syscalls"
		if ktrace -i -f "$work_path/ape-smoke.ktrace" "$ape_shell" "$smoke_path"; then
			trace_status=0
		else
			trace_status=$?
		fi
		echo "OpenBSD APE syscall trace exit=$trace_status" >&2
		kdump -f "$work_path/ape-smoke.ktrace" >&2
	fi
	exit 1
fi

report_phase "run version command"
version=$(run_binary "$ape_path" --version)
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
report_phase "run host environment task"
output=$(run_binary "$ape_path" --env KAME_PLATFORM_ENV=passed -f Makefile.kmk platform-smoke)
if [ "$output" != "platform-smoke-ok" ]; then
	echo "APE task output mismatch: $output" >&2
	exit 1
fi
if [ "$(cat platform-env.txt)" != passed ]; then
	echo "APE task did not preserve the host process environment" >&2
	exit 1
fi

set +e
report_phase "run timeout task"
run_binary "$ape_path" --timeout 100 -f Makefile.kmk platform-timeout \
	>"$work_path/timeout.out" 2>"$work_path/timeout.err"
timeout_status=$?
set -e
if [ "$timeout_status" -eq 0 ] || [ -e timeout-marker ]; then
	echo "APE timeout did not stop and reap its recipe process" >&2
	cat "$work_path/timeout.err" >&2
	exit 1
fi

printf 'APE host conformance passed: %s\n' "$version"
