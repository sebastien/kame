//go:build ignore
#include <errno.h>
#include <dirent.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <sys/stat.h>
#include <sys/file.h>
#include <unistd.h>
#if defined(__COSMOPOLITAN__)
#define _COSMO_SOURCE
#include "libc/dce.h"
#endif

// Metadata is trusted only inside the locked, no-follow project store. Artifact
// lookups never traverse symlinks. Status: 1 missing, 2 changed, -1 I/O,
// -2 busy, -3 unsupported/unsafe. No C allocation crosses the public API.
struct km_own_store { int root, kame, records, lock; unsigned long serial; };
struct km_own_file { int fd; struct stat stamp; };

bool km_own_supported(void) {
#if defined(_WIN32) || defined(__wasi__)
    return false;
#elif defined(__COSMOPOLITAN__)
    return !IsWindows();
#else
    return true;
#endif
}

static char *km_own_text(so_String s) {
    if (s.len < 1 || memchr(s.ptr, 0, s.len)) return NULL;
    char *p = malloc(s.len + 1);
    if (p) { memcpy(p, s.ptr, s.len); p[s.len] = 0; }
    return p;
}

static bool km_own_component(const char *p) {
    return *p && strcmp(p, ".") && strcmp(p, "..");
}

static int km_own_parent(int root, char *p, char **base, int forbidden) {
    if (!p || *p == '/') return -3;
    int fd = dup(root);
    if (fd < 0) return -1;
    char *part = p, *slash;
    while ((slash = strchr(part, '/'))) {
        *slash = 0;
        if (!km_own_component(part)) { close(fd); return -3; }
        int next = openat(fd, part, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
        int error = errno;
        close(fd);
        if (next < 0) return error == ENOENT ? -4 : -3;
        // String spelling alone cannot exclude .kame on case-insensitive hosts.
        // Refuse entry into the actual metadata directory through any alias.
        if (forbidden >= 0) {
            struct stat directory, reserved;
            if (fstat(next, &directory) || fstat(forbidden, &reserved) ||
                (directory.st_dev == reserved.st_dev && directory.st_ino == reserved.st_ino)) {
                close(next); return -3;
            }
        }
        fd = next; part = slash + 1;
    }
    if (!km_own_component(part)) { close(fd); return -3; }
    *base = part;
    return fd;
}

static int km_own_dir(int parent, const char *name, bool create) {
    if (create && mkdirat(parent, name, 0700) && errno != EEXIST) return -1;
    int fd = openat(parent, name, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (fd < 0) return errno == ENOENT ? -4 : -3;
    return fd;
}

km_own_store *km_own_open(so_String root, bool create, bool wait, int *status) {
    *status = -3;
#if defined(_WIN32) || defined(__wasi__)
    (void)root; (void)create; (void)wait;
    return NULL;
#else
#if defined(__COSMOPOLITAN__)
    if (IsWindows()) return NULL;
#endif
    char *name = km_own_text(root);
    if (!name || name[0] != '/') { free(name); return NULL; }
    int fd = open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    char *base = NULL;
    int parent = fd < 0 ? -1 : km_own_parent(fd, name + 1, &base, -1);
    int project = parent < 0 ? -1 : openat(parent, base, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (parent >= 0) close(parent);
    if (fd >= 0) close(fd);
    free(name);
    if (project < 0) return NULL;
    int kame = km_own_dir(project, ".kame", create);
    int records = kame < 0 ? kame : km_own_dir(kame, "outputs", create);
    if (records < 0) { if (kame >= 0) close(kame); close(project); *status = records == -4 ? 1 : records; return NULL; }
    // ponytail: one exclusive project lock serializes producers and cleanup;
    // introduce finer build leases only if concurrent invocation demand warrants it.
    int flags = O_RDWR | O_NOFOLLOW | O_CLOEXEC;
    if (create) flags |= O_CREAT;
    int lock = openat(records, "lock", flags, 0600);
    struct stat info;
    if (lock < 0 || fstat(lock, &info) || !S_ISREG(info.st_mode)) {
        if (lock >= 0) close(lock);
        close(records); close(kame); close(project); *status = -3; return NULL;
    }
    if (flock(lock, wait ? LOCK_EX : LOCK_EX | LOCK_NB)) {
        int failure = errno;
        close(lock); close(records); close(kame); close(project);
        *status = failure == EINTR ? -4 : -2; return NULL;
    }
    km_own_store *s = calloc(1, sizeof(*s));
    if (!s) { close(lock); close(records); close(kame); close(project); *status = -1; return NULL; }
    s->root = project; s->kame = kame; s->records = records; s->lock = lock;
    *status = 0; return s;
#endif
}

void km_own_close(km_own_store *s) {
    if (!s) return;
    close(s->lock); close(s->records); close(s->kame); close(s->root); free(s);
}

static void km_own_u64(unsigned char *p, uint64_t n) {
    for (int i = 0; i < 8; ++i) p[i] = n >> (8 * i);
}

static int64_t km_own_mtime(struct stat *s) {
#if defined(__APPLE__)
    return (int64_t)s->st_mtimespec.tv_sec * 1000000000 + s->st_mtimespec.tv_nsec;
#else
    return (int64_t)s->st_mtim.tv_sec * 1000000000 + s->st_mtim.tv_nsec;
#endif
}
static int64_t km_own_ctime(struct stat *s) {
#if defined(__APPLE__)
    return (int64_t)s->st_ctimespec.tv_sec * 1000000000 + s->st_ctimespec.tv_nsec;
#else
    return (int64_t)s->st_ctim.tv_sec * 1000000000 + s->st_ctim.tv_nsec;
#endif
}
static void km_own_metadata(struct stat *s, unsigned char *p) {
    km_own_u64(p, s->st_dev); km_own_u64(p + 8, s->st_ino);
    km_own_u64(p + 16, s->st_mode); km_own_u64(p + 24, s->st_size);
    km_own_u64(p + 32, km_own_mtime(s)); km_own_u64(p + 40, km_own_ctime(s));
}

int km_own_root(km_own_store *s, so_Slice data) {
    struct stat info;
    if (!s || data.len != 16 || fstat(s->root, &info)) return -1;
    km_own_u64(data.ptr, info.st_dev);
    km_own_u64((unsigned char *)data.ptr + 8, info.st_ino);
    return 0;
}

km_own_file *km_own_read(km_own_store *s, so_String name, bool artifact, int *status) {
    *status = -3;
    char *text = km_own_text(name), *base = NULL;
    if (!s || !text) { free(text); return NULL; }
    int parent = km_own_parent(artifact ? s->root : s->records, text, &base, artifact ? s->kame : -1);
    if (parent < 0) { free(text); *status = parent == -4 ? 1 : parent; return NULL; }
    int fd = openat(parent, base, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
    int error = errno;
    close(parent); free(text);
    if (fd < 0) { *status = error == ENOENT ? 1 : -3; return NULL; }
    struct stat info;
    if (fstat(fd, &info) || !S_ISREG(info.st_mode)) { close(fd); return NULL; }
    km_own_file *f = malloc(sizeof(*f));
    if (!f) { close(fd); *status = -1; return NULL; }
    f->fd = fd; f->stamp = info; *status = 0; return f;
}
int64_t km_own_size(km_own_file *f) { return f->stamp.st_size; }
int km_own_stamp(km_own_file *f, so_Slice data) {
    if (!f || data.len != 48) return -1;
    km_own_metadata(&f->stamp, data.ptr); return 0;
}
int km_own_bytes(km_own_file *f, so_Slice data) {
    ssize_t n;
    do { n = read(f->fd, data.ptr, data.len); } while (n < 0 && errno == EINTR);
    return (int)n;
}
bool km_own_stable(km_own_file *f) {
    struct stat info;
    unsigned char a[48], b[48];
    if (fstat(f->fd, &info)) return false;
    km_own_metadata(&info, a); km_own_metadata(&f->stamp, b);
    return !memcmp(a, b, sizeof(a));
}
void km_own_file_close(km_own_file *f) { if (f) { close(f->fd); free(f); } }

int km_own_write(km_own_store *s, so_String name, so_Slice data) {
    char *text = km_own_text(name);
    if (!s || !text || strchr(text, '/')) { free(text); return -3; }
    char tmp[80];
    snprintf(tmp, sizeof(tmp), ".tmp-%ld-%lu", (long)getpid(), ++s->serial);
    int fd = openat(s->records, tmp, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0600);
    if (fd < 0) { free(text); return -1; }
    size_t n = 0;
    while (n < (size_t)data.len) {
        ssize_t wrote = write(fd, (unsigned char *)data.ptr + n, data.len - n);
        if (wrote < 0 && errno == EINTR) continue;
        if (wrote <= 0) break;
        n += wrote;
    }
    int result = n == (size_t)data.len && !fsync(fd) ? 0 : -1;
    if (close(fd)) result = -1;
    if (!result && renameat(s->records, tmp, s->records, text)) result = -1;
    if (!result && fsync(s->records)) result = -1;
    if (result) unlinkat(s->records, tmp, 0);
    free(text); return result;
}
int km_own_remove(km_own_store *s, so_String name) {
    char *text = km_own_text(name);
    if (!s || !text || strchr(text, '/')) { free(text); return -3; }
    struct stat info;
    int result = fstatat(s->records, text, &info, AT_SYMLINK_NOFOLLOW);
    if (result && errno == ENOENT) { free(text); return 0; }
    if (result || !S_ISREG(info.st_mode)) { free(text); return -3; }
    result = unlinkat(s->records, text, 0) || fsync(s->records) ? -1 : 0;
    free(text); return result;
}
int km_own_list(km_own_store *s, so_Slice data) {
    int fd = openat(s->records, ".", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    DIR *dir = fd < 0 ? NULL : fdopendir(fd);
    if (!dir) { if (fd >= 0) close(fd); return -1; }
    size_t used = 0;
    struct dirent *entry;
    errno = 0;
    while ((entry = readdir(dir))) {
        size_t len = strlen(entry->d_name);
        if (len < 7 || strcmp(entry->d_name + len - 7, ".record")) continue;
        if (data.len && used + len + 1 <= (size_t)data.len) memcpy((char *)data.ptr + used, entry->d_name, len + 1);
        used += len + 1;
        if (used > 16 * 1024 * 1024) { closedir(dir); return -1; }
    }
    int error = errno; closedir(dir);
    return error ? -1 : (int)used;
}

// The rename captures whichever object currently occupies the public name.
// Callers revalidate that private captured object before unlinking it. A durable
// recovery receipt is written before calling this operation.
int km_own_capture(km_own_store *s, so_String name, so_String token, so_Slice expected) {
    char *text = km_own_text(name), *target = km_own_text(token), *base = NULL;
    if (!s || !text || !target || strchr(target, '/') || expected.len != 48) { free(text); free(target); return -3; }
    int parent = km_own_parent(s->root, text, &base, s->kame);
    if (parent < 0) { free(text); free(target); return parent == -4 ? 1 : parent; }
    struct stat info, occupied;
    unsigned char stamp[48];
    int result = fstatat(parent, base, &info, AT_SYMLINK_NOFOLLOW);
    if (result) result = errno == ENOENT ? 1 : -1;
    else {
        km_own_metadata(&info, stamp);
        if (!S_ISREG(info.st_mode) || memcmp(stamp, expected.ptr, sizeof(stamp))) result = 2;
        else if (!fstatat(s->records, target, &occupied, AT_SYMLINK_NOFOLLOW) || errno != ENOENT) result = -3;
        else if (renameat(parent, base, s->records, target)) result = -1;
        else if (fsync(parent) || fsync(s->records)) result = -1;
    }
    close(parent); free(text); free(target); return result;
}
int km_own_restore(km_own_store *s, so_String name, so_String token) {
    char *text = km_own_text(name), *target = km_own_text(token), *base = NULL;
    if (!s || !text || !target || strchr(target, '/')) { free(text); free(target); return -3; }
    int parent = km_own_parent(s->root, text, &base, s->kame);
    int result = -1;
    if (parent >= 0) {
        // linkat is no-replace, unlike rename. If another writer has claimed the
        // public name, retain recovery bytes and never overwrite that writer.
        if (!linkat(s->records, target, parent, base, 0) && !fsync(parent) &&
            !unlinkat(s->records, target, 0) && !fsync(s->records)) result = 0;
        close(parent);
    }
    free(text); free(target); return result;
}
