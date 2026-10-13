// Run from the repository root:
// Compile tools/native/test-posix.c with clang -std=gnu11 -Wall -Wextra -Werror
// -fsanitize=address,undefined, then run the resulting binary.
// Also fat-link with build/tools/cosmocc-4.0.2/bin/cosmocc -O0 -std=gnu11:
// Windows-only symbols must not leak into the aarch64 image without optimization.
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <signal.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

typedef unsigned char so_byte;
typedef intptr_t so_int;
typedef struct { void *ptr; so_int len, cap; } so_Slice;
typedef struct { const char *ptr; so_int len; } so_String;
#include "../../src/go/kame/host/posix/posix.h"

static int sync_files, sync_directories, sync_error, sync_interrupted;
static int probe_fsync(int fd) {
    struct stat info;
    assert(fstat(fd, &info) == 0);
    if (S_ISDIR(info.st_mode)) sync_directories++; else sync_files++;
    if (sync_interrupted) { sync_interrupted = 0; errno = EINTR; return -1; }
    if (sync_error) { errno = sync_error; return -1; }
    return fsync(fd);
}
#if !defined(__COSMOPOLITAN__)
static bool full_waitid_status;
static int observed_exit_status;
static int probe_waitid(idtype_t type, id_t id, siginfo_t *info, int options) {
    int result = waitid(type, id, info, options);
    // OpenBSD preserves the full exit(int); Linux already truncates it.
    if (!result && info->si_pid && info->si_code == CLD_EXITED && full_waitid_status)
        info->si_status = observed_exit_status;
    return result;
}
#define waitid probe_waitid
#endif
#define fsync probe_fsync
#include "../../src/go/kame/host/posix/posix.c"
#undef fsync
#if !defined(__COSMOPOLITAN__)
#undef waitid

static void test_full_waitid_exit_status(void) {
    int statuses[] = {256, 257, -1};
    for (size_t i = 0; i < sizeof(statuses) / sizeof(*statuses); i++) {
        observed_exit_status = statuses[i]; full_waitid_status = true;
        pid_t pid = fork(); assert(pid >= 0);
        if (!pid) { assert(setpgid(0, 0) == 0); _exit(statuses[i]); }
        setpgid(pid, pid);
        km_host *host = km_host_new(); assert(host);
        host->processes = calloc(1, sizeof(km_process)); assert(host->processes);
        host->len = host->cap = 1;
        km_process *p = host->processes;
        p->id = 23; p->pid = pid; p->started = true;
        p->outfd = p->errfd = p->execfd = -1;
        p->stages = calloc(1, sizeof(km_stage)); assert(p->stages);
        p->stage_count = 1; p->stages[0].pid = pid;
        km_event event = {0}; bool terminal = false;
        for (int n = 0; !terminal && n < 1000; n++) {
            assert(km_host_pump(host, 1) == 0);
            terminal = km_host_next(host, &event);
        }
        assert(terminal && event.kind == KM_TERMINAL && event.outcome == KM_EXITED);
        assert(event.stageCount == 1 && event.status == (statuses[i] & 0xff));
        assert(km_event_stage_field(&event, 0, 0) == event.status);
        assert(!host->len);
        km_event_free(&event); km_host_free(host);
    }
    full_waitid_status = false;
}
#endif

int main(void) {
#if !defined(__COSMOPOLITAN__)
    test_full_waitid_exit_status();
#endif
    FILE *file = tmpfile();
    assert(file && fwrite("storage", 1, 7, file) == 7);
    assert(km_file_sync(file) == 0 && sync_files == 1);
    sync_error = EIO;
    assert(km_file_sync(file) == EIO && sync_files == 2);
    assert(km_file_sync_parent((so_String){".", 1}) == EIO && sync_directories == 1);
    sync_error = 0;
    assert(km_file_sync_parent((so_String){".", 1}) == 0 && sync_directories == 2);
    assert(km_file_sync(NULL) == EINVAL);
    sync_interrupted = 1;
    assert(km_file_sync(file) == 0 && sync_files == 4);
    sync_interrupted = 1;
    assert(km_file_sync_parent((so_String){".", 1}) == 0 && sync_directories == 4);
    assert(fclose(file) == 0);

    km_host *host = km_host_new();
    assert(host);
    km_event_force_allocation_failure(0);
    assert(km_spawn_failed(host, 17, "rejected") == -1);
    assert(km_host_start(host, 18, (so_Slice){0}, (so_Slice){0}, (so_String){0}, (so_Slice){0}, 0, 0, false) == -2);
    km_event event;
    assert(km_host_next(host, &event) && event.id == 17 && event.kind == KM_TERMINAL && event.outcome == KM_FAILED);
    assert(event.diagnosticCode && event.diagnostic);
    km_event_free(&event);
    assert(!km_host_next(host, &event));

    // Reallocation of the process array must not invalidate pending events.
    host->processes = calloc(1, sizeof(km_process)); host->len = host->cap = 1;
    km_process *p = host->processes;
    p->id = 19; p->reaped = true; p->outfd = p->errfd = p->execfd = -1;
    assert(km_push(host, p, (km_event){.kind = KM_STARTED, .id = 19}));
    p->stdout_data = malloc(3); memcpy(p->stdout_data, "out", 3); p->stdout_len = 3;
    km_emit_terminal(host, p, KM_EXITED, NULL);
    host->processes = realloc(host->processes, 8 * sizeof(km_process)); host->cap = 8;
    assert(host->processes);
    assert(km_host_next(host, &event) && event.kind == KM_STARTED);
    km_event_free(&event);
    assert(km_host_next(host, &event) && event.kind == KM_TERMINAL && event.outcome == KM_FAILED && !event.output && !event.stdoutLen);
    km_event_free(&event);
    assert(!host->len && !km_host_next(host, &event));
    km_event_force_allocation_failure(-1);

    // Shared enqueue handles both missing chunk copies and node failures, and
    // only queued chunks contribute to backpressure accounting.
    for (int missing_copy = 0; missing_copy < 2; missing_copy++) {
        p = host->processes; memset(p, 0, sizeof(*p)); host->len = 1;
        p->id = 20 + missing_copy; p->reaped = true;
        p->outfd = p->errfd = p->execfd = -1;
        so_byte *first = km_copy("first", 5);
        assert(km_push(host, p, (km_event){.kind = KM_STDOUT, .id = p->id, .data = first, .dataLen = 5}));
        assert(p->queued == 5);
        so_byte *second = missing_copy ? NULL : km_copy("second", 6);
        km_event_force_allocation_failure(0);
        assert(!km_push(host, p, (km_event){.kind = KM_STDERR, .id = p->id, .data = second, .dataLen = 6}));
        assert(p->terminal && p->queued == 5);
        assert(km_host_next(host, &event) && event.kind == KM_STDOUT && event.dataLen == 5);
        assert(p->queued == 0); km_event_free(&event);
        assert(km_host_next(host, &event) && event.kind == KM_TERMINAL && event.outcome == KM_FAILED);
        assert(event.diagnosticCode && event.diagnostic); km_event_free(&event);
        assert(!host->len && !km_host_next(host, &event));
        km_event_force_allocation_failure(-1);
    }

    // Freeing an undrained host owns and frees both pending events and copies.
    p = host->processes; memset(p, 0, sizeof(*p)); host->len = 1;
    p->id = 22; p->reaped = true; p->outfd = p->errfd = p->execfd = -1;
    km_event_force_allocation_failure(0);
    assert(km_push(host, p, (km_event){.kind = KM_STARTED, .id = p->id}));
    km_emit_terminal(host, p, KM_FAILED, "test cleanup");
    km_event_force_allocation_failure(-1);
    km_host_free(host);
    puts("native event ownership and real fsync probes passed (not a power-loss test)");
}
