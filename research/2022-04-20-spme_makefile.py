# SEE: https://github.com/sebastien/spme/blob/main/Makefile
SOURCES_PY = wildcard("src/py/*/*.py")
SOURCES_OBSERVABLE = (
    "@sebastien/boilerplate",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
    "NNNNNNNNNNNNNNNN",
)

SOURCES_OBSERVABLE_TRANSITIVE = (
    shell("env NORECURSE=true make build/sources.lst > /dev/null")
    if env.NORECURSE
    else None
)

LIB_JS = (
    remap(SOURCES_OBSERVABLE, "%", "=lib/js/%.js"),
    remap(SOURCES_OBSERVABLE_TRANSITIVE, "%", "=lib/js/%.js"),
    "domish.js",
)

RUN_DEPS = LIB_JS



# NOTE: Dependencies can be explicit, but they'll also be extracted from the declarations
@task(RUN_DEPS)
def run():
    shell("asdasdd")

@rule
def run(deps=RUN_DEPS) -> None:
