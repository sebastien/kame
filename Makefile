# == Kame Bootstrap script
# Install  kame's GitHub release if needed, then forward goals to it.
# Kame reads Makefile.kmk. BSD make and GNU make. `make` runs the default goal.
KAME_VERSION ?= 0.3.1
KAME ?= $(HOME)/.cache/kame/bin/kame-$(KAME_VERSION)
KAME_RELEASE_URL ?= https://github.com/sebastien/kame/releases/download/v$(KAME_VERSION)

.PHONY: default release FORCE $(.TARGETS)
default: $(KAME)
	@$(RUN)
# Publish outside Kame's progress display; the script builds only for new releases.
release: FORCE
	@set --; \
		if [ -n "$${repo:-}" ]; then set -- "$$@" --repo "$$repo"; fi; \
		if [ -n "$${notes:-}" ]; then set -- "$$@" --notes-file "$$notes"; fi; \
		exec ./src/sh/release.sh "$$@"
$(MAKEFILE_LIST): ;
$(KAME):
	@$(FETCH)
%: $(KAME) FORCE
	@$(RUN)
FORCE: ;
.DEFAULT:
	@$(RUN)
.BEGIN:
	@if [ ! -x "$(KAME)" ]; then $(FETCH); fi
FETCH = set -eu; mkdir -p "$$(dirname "$(KAME)")"; tmp="$(KAME).tmp"; trap 'rm -f "$$tmp"' EXIT; \
	url="$(KAME_RELEASE_URL)/kame.com"; \
	if command -v curl >/dev/null 2>&1; then curl -fsSL "$$url" -o "$$tmp"; \
	elif command -v wget >/dev/null 2>&1; then wget -q -O "$$tmp" "$$url"; \
	elif command -v fetch >/dev/null 2>&1; then fetch -o "$$tmp" "$$url"; \
	else printf '%s\n' 'kame: no downloader (curl)' >&2; exit 1; fi; \
	chmod +x "$$tmp"; mv "$$tmp" "$(KAME)"
# Read bindings as data, never as shell source. GNU doubles dollars in MAKEFLAGS;
# BSD expands them there, but exports the original command-line variable values.
# The test suite prebuilds these artifacts before holding the project lock.
# Nested test requests must not start another Kame against that same lock.
RUN = goal="$@"; \
	if [ -n "$${KAME_TEST_PREBUILT:-}" ] && [ "$$(pwd -P)" = "$$KAME_TEST_PREBUILT" ]; then \
		case "$$goal" in \
			wasm) [ -f build/wasm/kame.wasm ] && exit 0; exit 1 ;; \
			dist-wasm) [ -f dist/kame.wasm ] && [ -f dist/kame.js ] && exit 0; exit 1 ;; \
			dist/kame) [ -x dist/kame ] && exit 0; exit 1 ;; \
			dist/kame-windows-x64.zip) [ -f dist/kame-windows-x64.zip ] && exit 0; exit 1 ;; \
		esac; \
	fi; \
	eval "$$(awk -v gnu="$(MAKE_VERSION)" 'function bind() { \
		if (token !~ /^[A-Za-z_][A-Za-z0-9_]*=/ || token ~ /^(KAME|KAME_VERSION|KAME_RELEASE_URL)=/) return; \
		if (!gnu) { name = substr(token, 1, index(token, "=")); token = name ENVIRON[substr(name, 1, length(name) - 1)]; } \
		printf "set -- \"$$@\" \047"; \
		for (j = 1; j <= length(token); j++) { \
			c = substr(token, j, 1); if (c == "\047") printf "\047\\\047\047"; else printf "%s", c; \
		} printf "\047\n"; \
	} BEGIN { \
		gnu = gnu ~ /\./; s = ENVIRON["MAKEFLAGS"]; token = ""; \
		for (i = 1; i <= length(s); i++) { \
			c = substr(s, i, 1); \
			if (c == "\\") token = token substr(s, ++i, 1); \
			else if (c ~ /[ \t]/) { bind(); token = ""; } \
			else { if (gnu && c == "$$" && substr(s, i + 1, 1) == "$$") i++; token = token c; } \
		} bind(); }')"; \
	exec "$(KAME)" "$$goal" "$$@"
# EOF
