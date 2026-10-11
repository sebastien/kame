// Linux acceptance-only LD_PRELOAD hooks. Each fault is armed by a marker in
// the ledger and consumed once. Production builds never link this file.
#define _GNU_SOURCE
#include <dlfcn.h>
#include <fcntl.h>
#include <signal.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

static int suffix(const char *name, const char *ending) {
    size_t length = strlen(name), tail = strlen(ending);
    return length >= tail && !strcmp(name + length - tail, ending);
}

static const char *take_fault(int dir) {
    const char *mode = getenv("KAME_CLEAN_TEST_FAULT");
    struct stat info;
    if (!mode || fstatat(dir, "test-fault", &info, AT_SYMLINK_NOFOLLOW)) return NULL;
    int (*remove_file)(int, const char *, int) = dlsym(RTLD_NEXT, "unlinkat");
    if (remove_file(dir, "test-fault", 0) || fsync(dir)) return NULL;
    return mode;
}

static void put_file(int dir, const char *name, const char *text) {
    int fd = openat(dir, name, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0600);
    if (fd < 0 || write(fd, text, strlen(text)) != (ssize_t)strlen(text) || fsync(fd)) abort();
    close(fd);
}

int renameat(int olddir, const char *oldname, int newdir, const char *newname) {
    int (*move)(int, const char *, int, const char *) = dlsym(RTLD_NEXT, "renameat");
    const char *requested = getenv("KAME_CLEAN_TEST_FAULT");
    const char *mode = suffix(newname, ".artifact") && requested && strcmp(requested, "retire-crash") ? take_fault(newdir) : NULL;
    if (mode && (!strcmp(mode, "replace-capture") || !strcmp(mode, "replace-occupied"))) {
        // Substitute after Kame's no-follow stat but before its atomic capture.
        if (move(olddir, oldname, olddir, "test-original")) abort();
        put_file(olddir, oldname, "replacement\n");
    }
    int result = move(olddir, oldname, newdir, newname);
    if (!result && mode) {
        if (!strcmp(mode, "cancel-capture")) raise(SIGTERM);
        if (!strcmp(mode, "replace-occupied")) put_file(olddir, oldname, "new-writer\n");
    }
    return result;
}

int unlinkat(int dir, const char *name, int flags) {
    int (*remove_file)(int, const char *, int) = dlsym(RTLD_NEXT, "unlinkat");
    const char *requested = getenv("KAME_CLEAN_TEST_FAULT");
    const char *mode = suffix(name, ".record") && requested && !strcmp(requested, "retire-crash") ? take_fault(dir) : NULL;
    if (mode && !strcmp(mode, "retire-crash")) raise(SIGKILL);
    return remove_file(dir, name, flags);
}
