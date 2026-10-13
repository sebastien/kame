// Linux policy/native checks, not Windows runtime conformance:
// clang -std=gnu11 -Wall -Wextra -Werror -fsanitize=address,undefined
// tools/native/test-argv.c -o /tmp/opencode/test-argv && /tmp/opencode/test-argv
// Fat-link the same source with cosmocc -O0 and -O3 (including aarch64).
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

typedef unsigned char so_byte;
typedef intptr_t so_int;
typedef struct { void *ptr; so_int len, cap; } so_Slice;
typedef struct { const char *ptr; so_int len; } so_String;
#include "../../src/go/kame/host/posix/posix.h"

static int allocations_left = -1;
static void *probe_malloc(size_t size) {
    if (allocations_left == 0) { errno = ENOMEM; return NULL; }
    if (allocations_left > 0) allocations_left--;
    return malloc(size);
}
#define malloc probe_malloc
#include "../../src/go/kame/host/posix/posix.c"
#undef malloc

typedef struct {
    const char **paths;
    const int *errors;
    size_t count, at;
} candidates;

static int candidate(const char *path, void *opaque) {
    candidates *probe = opaque;
    assert(probe->at < probe->count);
    if (strcmp(path, probe->paths[probe->at])) {
        fprintf(stderr, "candidate %zu: got <%s>, expected <%s>\n", probe->at, path, probe->paths[probe->at]);
        abort();
    }
    return probe->errors[probe->at++];
}

static void check(const char *name, char **env, const char *cwd, bool windows,
                  const char **paths, const int *errors, size_t count, int expected) {
    candidates probe = {paths, errors, count, 0};
    assert(km_argv_lookup(name, env, cwd, windows, candidate, &probe) == expected);
    assert(probe.at == count);
}

static void policy(void) {
    char native_drive[32] = "/D/work", native_root[4] = "/C";
    char native_unc[32] = "\\\\server\\share", native_empty[2] = "";
    km_argv_native_cwd(native_drive); km_argv_native_cwd(native_root);
    km_argv_native_cwd(native_unc); km_argv_native_cwd(native_empty);
    assert(!strcmp(native_drive, "D:/work") && !strcmp(native_root, "C:/"));
    assert(!strcmp(native_unc, "\\\\server\\share") && !*native_empty);
    char native_rooted[16] = "\\C\\work";
    km_argv_native_cwd(native_rooted);
    assert(!strcmp(native_rooted, "\\C\\work"));
    char mixed_rooted[16] = "/C\\work";
    km_argv_native_cwd(mixed_rooted);
    assert(!strcmp(mixed_rooted, "/C\\work"));
    char *rooted_cwd = km_argv_windows_path("D:/parent", native_rooted);
    char *mixed_cwd = km_argv_windows_path("D:/parent", mixed_rooted);
    assert(rooted_cwd && !strcmp(rooted_cwd, "D:/C/work"));
    assert(mixed_cwd && !strcmp(mixed_cwd, "D:/C/work"));
    free(rooted_cwd); free(mixed_cwd);
    char *handoff_env[] = {"PATHEXT=.EXE", NULL};
    const char *handoff[] = {"C:/work/tools/tool.EXE"};
    int handoff_ok[] = {0};
    check("/C/work/tools/tool.EXE", handoff_env, "D:/stage", true, handoff, handoff_ok, 1, 0);
    check("C:/work/tools/tool.EXE", handoff_env, "/C/work", true, handoff, handoff_ok, 1, 0);
    const char *cwd_alias[] = {"C:/work/tool.EXE"};
    check("tool.EXE", handoff_env, "/C/work", true, cwd_alias, handoff_ok, 1, 0);
    const char *root_cwd[] = {"C:/tool.EXE"};
    check("tool.EXE", handoff_env, "/C", true, root_cwd, handoff_ok, 1, 0);
    char *crossdrive[] = {"PATH=/C/tools", "PATHEXT=.EXE", NULL};
    const char *crossdrive_path[] = {"C:/tools/tool.EXE"};
    check("tool", crossdrive, "/D/work", true, crossdrive_path, handoff_ok, 1, 0);
    const char *native_rooted_path[] = {"D:/C/tools/tool.EXE"};
    check("\\C\\tools\\tool.EXE", handoff_env, "D:/stage", true, native_rooted_path, handoff_ok, 1, 0);
    check("/C\\tools\\tool.EXE", handoff_env, "D:/stage", true, native_rooted_path, handoff_ok, 1, 0);
    const char *drive_root[] = {"C:/"};
    int root_error[] = {EACCES, ENOENT};
    const char *drive_root_errors[] = {"C:/", "C:/.EXE"};
    check("/C", handoff_env, "D:/stage", true, drive_root_errors, root_error, 2, EACCES);
    check("/C", handoff_env, "D:/stage", true, drive_root, handoff_ok, 1, 0);
    char *none[] = {NULL};
    const char *defaults[] = {"D:/work/tool.COM", "D:/work/tool.EXE", "D:/work/tool.BAT", "D:/work/tool.CMD", "D:/work/tool"};
    int absent[] = {ENOENT, ENOTDIR, ENOENT, ENOENT, ENOENT};
    check("tool", none, "D:/work", true, defaults, absent, 5, ENOENT);
    char *empty[] = {"pAtH=", "pAtHeXt=", NULL};
    check("tool", empty, "D:/work", true, defaults, absent, 5, ENOENT);
    int fallback[] = {ENOENT, ENOTDIR, EACCES, ENOENT, 0};
    check("tool", empty, "D:/work", true, defaults, fallback, 5, 0);
    int fallback_format[] = {ENOENT, ENOTDIR, EACCES, ENOENT, ENOEXEC};
    check("tool", empty, "D:/work", true, defaults, fallback_format, 5, ENOEXEC);
    char *first[] = {"PATHOTHER=ignored", "path=D:\\first", "PATH=D:\\second", "pathext=.custom", NULL};
    const char *first_path[] = {"D:/first/tool.custom"};
    int found[] = {0};
    check("tool", first, "C:/parent", true, first_path, found, 1, 0);
    char *custom[] = {"Path=C:\\bin;;rel;\\tools;\\\\server\\share", "PATHEXT=;.CUSTOM;bad;.;.EXE;", NULL};
    const char *ordered[] = {
        "C:/bin/tool.CUSTOM", "C:/bin/tool.EXE", "C:/bin/tool", "D:/work/tool.CUSTOM", "D:/work/tool.EXE", "D:/work/tool",
        "D:/work/rel/tool.CUSTOM", "D:/work/rel/tool.EXE", "D:/work/rel/tool", "D:/tools/tool.CUSTOM", "D:/tools/tool.EXE", "D:/tools/tool",
        "//server/share/tool.CUSTOM", "//server/share/tool.EXE", "//server/share/tool"};
    int denied[] = {ENOENT, EACCES, ENOTDIR, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT, ENOENT};
    check("tool", custom, "D:/work", true, ordered, denied, 15, EACCES);
    denied[14] = 0;
    check("tool", custom, "D:/work", true, ordered, denied, 15, 0);
    char *extension[] = {"PATHEXT=.CUSTOM;.EXE", "PATH=ignored", NULL};
    const char *explicit[] = {"D:/tools/tool.EXE", "D:/tools/tool.EXE.CUSTOM", "D:/tools/tool.EXE.EXE"};
    int explicit_errors[] = {ENOENT, ENOTDIR, EACCES};
    check("\\tools\\tool.EXE", extension, "D:/work", true, explicit, explicit_errors, 3, EACCES);
    int success[] = {0};
    check("/tools/tool.EXE", extension, "D:/work", true, explicit, success, 1, 0);
    const char *drive[] = {"C:/tools/tool", "C:/tools/tool.CUSTOM"};
    check("C:\\tools\\tool", extension, "D:/work", true, drive, success, 1, 0);
    int suffix_after_exact[] = {ENOENT, 0};
    check("C:\\tools\\tool", extension, "D:/work", true, drive, suffix_after_exact, 2, 0);
    int exact_format[] = {ENOEXEC};
    check("C:\\tools\\tool", extension, "D:/work", true, drive, exact_format, 1, ENOEXEC);
    const char *unc[] = {"//server/share/tool"};
    check("\\\\server\\share\\tool", extension, "D:/work", true, unc, success, 1, 0);
    const char *relative[] = {"D:/work/./tool"};
    check(".\\tool", extension, "D:/work", true, relative, success, 1, 0);
    check("C:tool", extension, "D:/work", true, NULL, NULL, 0, ENOTSUP);
    check("C:", extension, "D:/work", true, NULL, NULL, 0, ENOTSUP);
    check("\\tools\\tool", extension, "//server/share/work", true, NULL, NULL, 0, ENOTSUP);
    check("/tools/tool", extension, "//server/share/work", true, NULL, NULL, 0, ENOTSUP);
    check("\\C\\tools\\tool.EXE", handoff_env, "//server/share/work", true, NULL, NULL, 0, ENOTSUP);
    check("/C/tools/tool.EXE", handoff_env, "//server/share/work", true, crossdrive_path, handoff_ok, 1, 0);
    char *drive_relative[] = {"PATH=C:;C:tools;D:\\bin", "PATHEXT=.EXE", NULL};
    const char *valid[] = {"D:/bin/tool.EXE"};
    check("tool", drive_relative, "D:/work", true, valid, success, 1, 0);
    const char *batch[] = {"D:/work/tool.BAT"};
    int format[] = {ENOEXEC};
    check("tool.BAT", none, "D:/work", true, batch, format, 1, ENOEXEC);
    char *cmd[] = {"PATHEXT=.CMD;.EXE", NULL};
    const char *command[] = {"D:/work/tool.CMD"};
    check("tool", cmd, "D:/work", true, command, format, 1, ENOEXEC);
    int oom[] = {ENOMEM}, busy[] = {ETXTBSY};
    check("tool", none, "D:/work", true, defaults, oom, 1, ENOMEM);
    int denied_oom[] = {EACCES, ENOMEM};
    check("tool", none, "D:/work", true, defaults, denied_oom, 2, ENOMEM);
    check("tool", none, "D:/work", true, defaults, busy, 1, ETXTBSY);
    const char *posix_defaults[] = {"/bin/tool", "/usr/bin/tool"};
    check("tool", none, NULL, false, posix_defaults, absent, 2, ENOENT);
    char *wrong_case[] = {"Path=/ignored", NULL};
    check("tool", wrong_case, NULL, false, posix_defaults, absent, 2, ENOENT);
    char *posix[] = {"PATH=:/denied:/missing:", NULL};
    const char *posix_order[] = {"tool", "/denied/tool", "/missing/tool", "tool"};
    int posix_errors[] = {ENOENT, EACCES, ENOTDIR, ENOENT};
    check("tool", posix, NULL, false, posix_order, posix_errors, 4, EACCES);
    const char *plain[] = {"./tool"};
    check("./tool", posix, NULL, false, plain, format, 1, ENOEXEC);
    char *empty_posix[] = {"PATH=", NULL};
    const char *here[] = {"tool"};
    check("tool", empty_posix, NULL, false, here, success, 1, 0);
    check("", none, "D:/work", true, NULL, NULL, 0, ENOENT);
    const char *empty_name[] = {"/bin/", "/usr/bin/"};
    int empty_errors[] = {EACCES, EACCES};
    check("", none, NULL, false, empty_name, empty_errors, 2, EACCES);
    char *invalid_extensions[] = {"PATHEXT=.;invalid;;", NULL};
    const char *exact_only[] = {"D:/work/tool"};
    check("tool", invalid_extensions, "D:/work", true, exact_only, absent, 1, ENOENT);
    for (int i = 0; i < 4; i++) {
        // Fail the PATH buffer, Windows base/name, then suffix allocation.
        allocations_left = i;
        check("tool", none, "D:/work", true, NULL, NULL, 0, ENOMEM);
    }
    allocations_left = -1;
    allocations_left = 0;
    check("tool", none, NULL, false, NULL, NULL, 0, ENOMEM);
    char *argv[] = {"tool", NULL};
    km_exec_argv(argv, none);
    assert(errno == ENOMEM);
    allocations_left = -1;
}

static so_String text(const char *s) { return (so_String){s, (so_int)strlen(s)}; }

static void native_request(const char *directory, const char *name, const char *path, bool fails) {
    const char *argument = "literal ; $(echo injected) \" & |";
    so_String argv[] = {text(name), text(argument)};
    so_String environment[] = {text(path), text("MARK=request-env")};
    km_host *host = km_host_new();
    assert(host);
    assert(km_host_start(host, 7, (so_Slice){argv, 2, 2}, (so_Slice){0}, text(directory),
                         (so_Slice){environment, 2, 2}, 2000, 1024, true) == 0);
    bool terminal = false;
    for (int i = 0; !terminal && i < 300; i++) {
        assert(km_host_pump(host, 10) == 0);
        km_event event;
        while (km_host_next(host, &event)) {
            if (event.kind == KM_TERMINAL) {
                terminal = true;
                if (fails) assert(event.outcome == KM_FAILED && event.diagnosticLen);
                else {
                    char expected[128];
                    snprintf(expected, sizeof(expected), "%s:request-env", argument);
                    assert(event.outcome == KM_EXITED && event.status == 0);
                    assert(event.stdoutLen == (int)strlen(expected));
                    assert(!memcmp(event.output, expected, strlen(expected)));
                }
            }
            km_event_free(&event);
        }
    }
    assert(terminal);
    km_host_free(host);
}

static int exec_error(char **argv, char **envp) {
    int channel[2]; assert(pipe(channel) == 0);
    pid_t pid = fork(); assert(pid >= 0);
    if (!pid) {
        close(channel[0]);
        km_exec_argv(argv, envp);
        int error = errno;
        assert(write(channel[1], &error, sizeof(error)) == sizeof(error));
        _exit(0);
    }
    close(channel[1]);
    int error = 0, status;
    assert(read(channel[0], &error, sizeof(error)) == sizeof(error));
    close(channel[0]);
    assert(waitpid(pid, &status, 0) == pid && WIFEXITED(status) && !WEXITSTATUS(status));
    return error;
}

int main(void) {
    policy();
    char directory[] = "/tmp/opencode/kame-argv-XXXXXX";
    assert(mkdtemp(directory));
    char file[PATH_MAX], path[PATH_MAX + 32];
    snprintf(file, sizeof(file), "%s/tool", directory);
    FILE *stream = fopen(file, "w"); assert(stream);
    assert(fputs("#!/bin/sh\nprintf '%s:%s' \"$1\" \"$MARK\"\n", stream) >= 0);
    assert(fclose(stream) == 0 && chmod(file, 0700) == 0);
    snprintf(path, sizeof(path), "PATH=/missing:%s", directory);
    native_request(directory, "tool", path, false);
    native_request(directory, "./tool", "PATH=/missing", false);
    native_request(directory, "tool", "PATH=", false);
    native_request(directory, "tool", "Path=/ignored", true);
    char *argv[] = {file, NULL}, *envp[] = {path, NULL};
    assert(chmod(file, 0600) == 0);
    assert(exec_error(argv, envp) == EACCES);
    argv[0] = "tool";
    assert(exec_error(argv, envp) == EACCES);
#if !defined(__COSMOPOLITAN__)
    // Native libc execve rejects plain text. Cosmo's POSIX execve instead
    // retries it via /bin/sh (libc/proc/execve-sysv.c); unchanged here. Windows
    // ntspawn has no such fallback, tested as an ENOEXEC policy callback above.
    assert(chmod(file, 0700) == 0);
    stream = fopen(file, "w"); assert(stream);
    assert(fputs("echo must-not-run-with-an-implicit-shell\n", stream) >= 0);
    assert(fclose(stream) == 0);
    assert(exec_error(argv, envp) == ENOEXEC);
    native_request(directory, "tool", path, true);
#endif
#if defined(__COSMOPOLITAN__)
    // Exercise the actual spawn callback on Linux with a cwd file action.
    posix_spawn_file_actions_t actions;
    posix_spawnattr_t attr;
    assert(posix_spawn_file_actions_init(&actions) == 0);
    assert(posix_spawnattr_init(&attr) == 0);
    assert(posix_spawn_file_actions_addchdir_np(&actions, directory) == 0);
    pid_t pid = -1;
    errno = EDOM;
    assert(km_spawn_argv(&pid, argv, envp, directory, &actions, &attr) == EACCES);
    assert(errno == EDOM && pid == -1);
    char *ok[] = {"true", NULL}, *bins[] = {"PATH=/bin:/usr/bin", NULL};
    assert(km_spawn_argv(&pid, ok, bins, directory, &actions, &attr) == 0);
    int status;
    assert(waitpid(pid, &status, 0) == pid && WIFEXITED(status) && !WEXITSTATUS(status));
    assert(posix_spawn_file_actions_destroy(&actions) == 0);
    assert(posix_spawnattr_destroy(&attr) == 0);
#endif
    assert(unlink(file) == 0 && rmdir(directory) == 0);
    puts("argv policy and Linux native checks passed (not Windows conformance)");
    return 0;
}
