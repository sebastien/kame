# =============================================================================
# Kame bootstrap for make
# =============================================================================
#
# This Makefile builds nothing itself. It locates a Kame executable, installs
# one when missing, then forwards the goals to Kame, which reads `Kamefile`.
# Copy it into any project that has a Kamefile. It is POSIX make and sh, so it
# works with GNU Make and BSD make alike.
#
#   make [GOAL...] [NAME=VALUE...]   ->  kame [-n] [--jobs=N] [NAME=VALUE...] [GOAL...]
#
# GNU and BSD make expose the goal list, so Kame runs once with every goal;
# other makes run Kame once per goal and cannot select a goal that names an
# existing file. `-n` and `-jN` are forwarded, as are command-line assignments
# other than the bootstrap settings below. Kame treats only `./`-prefixed goals
# as files; a goal containing `/` gets that prefix, so `make dist/kame` requests
# `./dist/kame`. GNU Make drops a leading `./` itself, so request a top-level
# file goal such as `./out` through `kame` directly.
#
# Executable selection, first match wins:
#   1. KAME=PATH                       explicit executable
#   2. ./src/go/kame/go.mod and `go`   Kame's own checkout: build from source
#                                      into .kame/bootstrap/source/kame
#   3. .kame/bootstrap/VERSION/kame    previously installed release
#   4. `kame` on PATH                  only when `kame --version` matches
#   5. GitHub release                  native binary for Linux/macOS on
#                                      x86_64/arm64, checked against SHA256SUMS
#
# Settings, from the command line or the environment:
#   KAME_VERSION      release to use (default: contents of ./VERSION)
#   KAME_REPOSITORY   GitHub repository (default: sebastien/kame)
#   KAME_RELEASE_URL  release asset base (default: its vVERSION download URL)
#   KAME_BOOTSTRAP    installation directory (default: .kame/bootstrap)
#   GO                Go executable for source builds (default: go)
#   KAME_RELEASE_PUBLIC_KEY
#                     trusted Ed25519 PEM key; when set, SHA256SUMS.sig must
#                     verify. Without it, SHA256SUMS proves integrity only.
#   KAME_MAKEFILE     this file's name, when it is not `Makefile`
# =============================================================================

.POSIX:
.SUFFIXES:

KAME_MAKEFILE = Makefile

# The bootstrap is the sh script embedded below in `#| ` lines. Running it via
# `sh -c` keeps the terminal on Kame's stdin; `+` runs it under `make -n`, and
# Kame then performs the dry run.
KAME_RUN = KAME_TARGET='$@' KAME_GOALS='$(MAKECMDGOALS) $(.TARGETS)' \
	KAME='$(KAME)' KAME_VERSION='$(KAME_VERSION)' KAME_REPOSITORY='$(KAME_REPOSITORY)' \
	KAME_RELEASE_URL='$(KAME_RELEASE_URL)' KAME_BOOTSTRAP='$(KAME_BOOTSTRAP)' GO='$(GO)' \
	KAME_RELEASE_PUBLIC_KEY='$(KAME_RELEASE_PUBLIC_KEY)' \
	sh -c "$$(sed -n -e 's/^.| //p' -e 's/^.|$$//p' '$(KAME_MAKEFILE)')"

__kame: __kame_force
	+@$(KAME_RUN)

# GNU Make names the goals in MAKECMDGOALS, BSD make in .TARGETS.
__kame_goals $(MAKECMDGOALS) $(.TARGETS): __kame_force
	+@$(KAME_RUN)

# Other makes reach goals without rules one at a time.
.DEFAULT:
	+@$(KAME_RUN)

__kame_force:

Makefile: ;

#| set -eu
#| set -f
#|
#| case "$KAME_TARGET" in __kame_force | Makefile | makefile) exit 0 ;; esac
#|
#| # Make flags and command-line assignments arrive in MAKEFLAGS: GNU Make
#| # leads with a bare flag cluster (`ns`), BSD make uses `-n -j 3`. Both
#| # escape spaces in values with a backslash.
#| set --
#| soh=$(printf '\001')
#| first_word=1
#| jobs_next=
#| for word in $(printf '%s' "${MAKEFLAGS:-}" | sed "s/\\\\ /$soh/g"); do
#| 	word=$(printf '%s' "$word" | sed -e "s/$soh/\\\\ /g" -e 's/\\\(.\)/\1/g')
#| 	if [ -n "$jobs_next" ]; then
#| 		jobs_next=
#| 		case "$word" in [1-9]*) set -- "$@" "--jobs=$word" ;; esac
#| 		continue
#| 	fi
#| 	case "$word" in
#| 	-j) jobs_next=1 ;;
#| 	-j[1-9]*) set -- "$@" "--jobs=${word#-j}" ;;
#| 	-n) set -- "$@" -n ;;
#| 	-*) ;;
#| 	*=*)
#| 		case "${word%%=*}" in
#| 		KAME | KAME_VERSION | KAME_REPOSITORY | KAME_RELEASE_URL | KAME_BOOTSTRAP | GO | KAME_MAKEFILE) ;;
#| 		'' | *[!A-Za-z0-9_.]*) ;;
#| 		*) set -- "$@" "$word" ;;
#| 		esac
#| 		;;
#| 	*[!A-Za-z]*) ;;
#| 	*n*) [ -z "$first_word" ] || set -- "$@" -n ;;
#| 	esac
#| 	first_word=
#| done
#|
#| # Only the first goal's recipe runs Kame, with every goal. Without a goal
#| # list, each goal runs Kame alone.
#| with_dot() {
#| 	case "$1" in ./* | ../* | /*) printf '%s' "$1" ;; */*) printf './%s' "$1" ;; *) printf '%s' "$1" ;; esac
#| }
#| first=
#| for goal in $KAME_GOALS; do
#| 	case "$goal" in __kame | __kame_goals | __kame_force) continue ;; esac
#| 	[ -n "$first" ] || first=$goal
#| 	set -- "$@" "$(with_dot "$goal")"
#| done
#| if [ -n "$first" ]; then
#| 	[ "${KAME_TARGET#./}" = "${first#./}" ] || exit 0
#| elif [ "$KAME_TARGET" != __kame ]; then
#| 	set -- "$@" "$(with_dot "$KAME_TARGET")"
#| fi
#|
#| version=${KAME_VERSION:-$(cat VERSION 2>/dev/null || :)}
#| home=${KAME_BOOTSTRAP:-.kame/bootstrap}
#| go=${GO:-go}
#| kame=${KAME:-}
#|
#| if [ -n "$kame" ]; then
#| 	:
#| elif [ -f src/go/kame/go.mod ] && command -v "$go" >/dev/null 2>&1; then
#| 	# Kame's own checkout always builds itself from its sources.
#| 	kame=$home/source/kame
#| 	if [ ! -x "$kame" ] || [ -n "$(find VERSION src/go/kame -newer "$kame" \
#| 		\( -name '*.go' -o -name go.mod -o -name go.sum -o -name VERSION \) \
#| 		! -name version_generated.go 2>/dev/null | head -n 1)" ]; then
#| 		root=$(pwd)
#| 		mkdir -p "$home/source"
#| 		# Mirrors the Kamefile's version_generated.go rule, which needs Kame.
#| 		generated=src/go/kame/cmd/kame/version_generated.go
#| 		build_id=$(git rev-parse --short=12 HEAD 2>/dev/null) || build_id=unknown
#| 		staged=$(mktemp "$generated.XXXXXX")
#| 		printf '// Code generated from repository/build metadata; DO NOT EDIT.\npackage main\n\nconst version = "%s"\nconst buildID = "%s"\nconst buildTime = "development"\n' \
#| 			"$(tr -d ' \t\r\n' <VERSION)" "$build_id" >"$staged"
#| 		if cmp -s "$staged" "$generated"; then rm -f "$staged"; else mv "$staged" "$generated"; fi
#| 		(cd src/go/kame && CGO_ENABLED=0 GOCACHE="${GOCACHE:-$root/build/go-cache}" \
#| 			"$go" build -o "$root/$kame" ./cmd/kame)
#| 	fi
#| else
#| 	[ -n "$version" ] || { echo 'kame: set KAME_VERSION or provide ./VERSION' >&2; exit 1; }
#| 	kame=$home/$version/kame
#| 	path=$(command -v kame 2>/dev/null || :)
#| 	if [ -x "$kame" ]; then
#| 		:
#| 	elif [ -n "$path" ] && [ "$("$path" --version 2>/dev/null | awk '{ print $2 }')" = "$version" ]; then
#| 		kame=$path
#| 	else
#| 		case "$(uname -s)" in
#| 		Linux) os=linux ;;
#| 		Darwin) os=darwin ;;
#| 		*) echo "kame: no native release for $(uname -s); install the npm package or set KAME=PATH" >&2; exit 1 ;;
#| 		esac
#| 		case "$(uname -m)" in
#| 		x86_64 | amd64) arch=x86_64 ;;
#| 		aarch64 | arm64) arch=arm64 ;;
#| 		*) echo "kame: no native release for $(uname -m); install the npm package or set KAME=PATH" >&2; exit 1 ;;
#| 		esac
#| 		asset=kame-$os-$arch
#| 		url=${KAME_RELEASE_URL:-https://github.com/${KAME_REPOSITORY:-sebastien/kame}/releases/download/v$version}
#| 		temporary=$(mktemp -d)
#| 		trap 'rm -rf "$temporary"' EXIT
#| 		fetch() {
#| 			if command -v curl >/dev/null 2>&1; then
#| 				curl -fsSL "$1" -o "$2"
#| 			elif command -v wget >/dev/null 2>&1; then
#| 				wget -q -O "$2" "$1"
#| 			else
#| 				echo 'kame: curl or wget is required to install Kame' >&2
#| 				return 1
#| 			fi || { echo "kame: download failed: $1" >&2; return 1; }
#| 		}
#| 		echo "kame: installing $asset $version from $url" >&2
#| 		fetch "$url/SHA256SUMS" "$temporary/SHA256SUMS"
#| 		fetch "$url/$asset" "$temporary/$asset"
#| 		if [ -n "${KAME_RELEASE_PUBLIC_KEY:-}" ]; then
#| 			fetch "$url/SHA256SUMS.sig" "$temporary/SHA256SUMS.sig"
#| 			openssl pkeyutl -verify -pubin -inkey "$KAME_RELEASE_PUBLIC_KEY" -rawin \
#| 				-in "$temporary/SHA256SUMS" -sigfile "$temporary/SHA256SUMS.sig" >/dev/null ||
#| 				{ echo 'kame: SHA256SUMS signature verification failed' >&2; exit 1; }
#| 		fi
#| 		expected=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1 }' "$temporary/SHA256SUMS")
#| 		if command -v sha256sum >/dev/null 2>&1; then
#| 			actual=$(sha256sum "$temporary/$asset" | cut -d' ' -f1)
#| 		else
#| 			actual=$(shasum -a 256 "$temporary/$asset" | cut -d' ' -f1)
#| 		fi
#| 		[ -n "$expected" ] && [ "$expected" = "$actual" ] ||
#| 			{ echo "kame: checksum mismatch for $asset" >&2; exit 1; }
#| 		chmod +x "$temporary/$asset"
#| 		mkdir -p "$home/$version"
#| 		mv "$temporary/$asset" "$kame"
#| 		rm -rf "$temporary"
#| 		trap - EXIT
#| 	fi
#| fi
#|
#| exec "$kame" "$@"
