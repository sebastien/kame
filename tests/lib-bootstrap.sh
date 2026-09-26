#!/usr/bin/env bash

# Shared test bootstrap. Test scripts source this file instead of repeating
# repository-relative loading of the assertion and CLI helper libraries.
TEST_BOOTSTRAP_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$TEST_BOOTSTRAP_ROOT/lib-testing.sh"
# shellcheck disable=SC1091
source "$TEST_BOOTSTRAP_ROOT/lib-cli.sh"
unset TEST_BOOTSTRAP_ROOT
