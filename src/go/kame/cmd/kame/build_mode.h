#ifndef KAME_BUILD_MODE_H
#define KAME_BUILD_MODE_H

#if (defined(KAME_BUILD_MODE_DEBUG) + defined(KAME_BUILD_MODE_SANITIZE) + defined(KAME_BUILD_MODE_RELEASE)) > 1
#error "select only one Kame build mode"
#endif

#if defined(KAME_BUILD_MODE_DEBUG)
#define KAME_ARTIFACT_MODE "debug"
#elif defined(KAME_BUILD_MODE_SANITIZE)
#define KAME_ARTIFACT_MODE "sanitize"
#elif defined(KAME_BUILD_MODE_RELEASE)
#define KAME_ARTIFACT_MODE "release"
#else
#define KAME_ARTIFACT_MODE "development"
#endif

static inline so_String kame_build_mode(void) {
    return (so_String){.ptr = KAME_ARTIFACT_MODE, .len = sizeof(KAME_ARTIFACT_MODE) - 1};
}

#endif
