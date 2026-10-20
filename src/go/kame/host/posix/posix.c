//go:build ignore
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <sys/stat.h>
#include <sys/ioctl.h>
#include <sys/file.h>
#if defined(__linux__) || defined(__COSMOPOLITAN__)
#include <sys/sysmacros.h>
#endif
#include <sys/wait.h>
#include <sys/mman.h>
#include <signal.h>
#if defined(__COSMOPOLITAN__)
#include <spawn.h>
#endif
#include <time.h>
#include <unistd.h>
#include <dirent.h>
#if defined(__linux__) && !defined(__COSMOPOLITAN__)
#include <sys/inotify.h>
#endif
#if defined(__COSMOPOLITAN__)
// Cosmocc 4.0.2 ships the syscall stubs but no public inotify header/wrappers.
// These Linux-only calls are guarded by IsLinux() below; never use syscall(),
// whose Cosmopolitan numbering is not Linux's syscall numbering.
extern int sys_inotify_init1(int);
extern int sys_inotify_add_watch(int, const char *, uint32_t);
#define inotify_init1 sys_inotify_init1
#define inotify_add_watch sys_inotify_add_watch
#define IN_NONBLOCK 0x800
#define IN_CLOEXEC 0x80000
#define IN_MODIFY 0x2
#define IN_ATTRIB 0x4
#define IN_MOVED_FROM 0x40
#define IN_MOVED_TO 0x80
#define IN_CREATE 0x100
#define IN_DELETE 0x200
#define IN_DELETE_SELF 0x400
#define IN_MOVE_SELF 0x800
#endif
#if defined(__COSMOPOLITAN__)
#define _COSMO_SOURCE
#include "libc/calls/internal.h"
#include "libc/calls/state.internal.h"
#include "libc/dce.h"
#include "libc/nt/dll.h"
#include "libc/nt/files.h"
#include "libc/nt/ipc.h"
#include "libc/nt/process.h"
#include "libc/nt/runtime.h"
#include "libc/proc/proc.h"
#include "libc/nt/thunk/msabi.h"
// Cosmopolitan 4.0.2 exposes the Linux syscall, not a public waitid() wrapper.
extern int sys_waitid(int, int, void *, int, void *);
#endif

// Event kind and outcome values must match posix.EventKind and posix.Outcome.
enum { KM_STARTED, KM_STDOUT, KM_STDERR, KM_TERMINAL, KM_SNAPSHOT };
enum { KM_RUNNING = 1, KM_WAIT_INPUT, KM_WAIT_OUTPUT, KM_COMPLETED, KM_STAGE_FAILED, KM_STAGE_CANCELLED, KM_STAGE_TIMEOUT };
enum { KM_EXITED, KM_TIMED_OUT, KM_CANCELLED, KM_FAILED };
enum { KM_QUEUE_LIMIT = 256 * 1024, KM_CHUNK = 16 * 1024, KM_GRACE_MS = 100 };
enum { KM_METRIC_FIELDS = 7, KM_METRIC_MODE = 0, KM_METRIC_BYTES, KM_METRIC_ITEMS,
       KM_METRIC_DROPS, KM_METRIC_HIGH_BYTES, KM_METRIC_HIGH_ITEMS, KM_METRIC_SATURATED };

// Host diagnostics use stable codes from docs/spec/011-diagnostics.md.
#define KM_HOSTF "HOST_FAIL"

typedef struct km_stage {
    pid_t pid;
    int64_t job;
    int64_t deadline;
    int status, signal, outcome, operator_kind;
    bool exited, reaped, spawned_in_job;
} km_stage;

typedef struct km_process {
    int64_t id;
    pid_t pid;
    int64_t job;
    int outfd, errfd, execfd;
    bool started, exited, reaped, terminating, killed, terminal;
    int outcome, status, signal;
    int64_t deadline, kill_deadline;
    int queued;
    so_byte *stdout_data, *stderr_data;
    int stdout_len, stderr_len, retain, stdout_cap, stderr_cap;
    bool stdout_truncated, stderr_truncated;
    bool direct;
    km_stage *stages;
    int stage_count;
    bool topology;
    bool collect_failures;
    int failed_stage;
    int64_t *metrics;
    int metric_count;
    int *live_states;
    int64_t snapshot_at;
    // Mandatory events survive queue allocation failure without allocating.
    km_event pending[2];
    uint64_t pending_sequence[2];
} km_process;

typedef struct km_event_node {
    km_event event;
    int64_t process_id;
    struct km_event_node *next;
    uint64_t sequence;
} km_event_node;

struct km_host {
    km_process *processes;
    int len, cap;
    km_event_node *first, *last;
    bool force_waitpid_failure;
    uint64_t sequence, rejected_sequence;
    km_event rejected;
    unsigned cache_lock_refs[256];
};

static int km_cache_lock_fd[256];
#if defined(__COSMOPOLITAN__)
static int64_t km_cache_lock_mutex[256];
#endif
static unsigned km_cache_lock_refs[256];
static bool km_cache_lock_initialized;
static bool km_lock_notice(int, so_String, so_String, const char *, int64_t);
static int64_t km_now(void);

#if defined(__COSMOPOLITAN__)
typedef int64_t (__msabi *km_create_job_object_fn)(void *, const char16_t *);
typedef int32_t (__msabi *km_assign_process_to_job_object_fn)(int64_t, int64_t);
typedef int32_t (__msabi *km_terminate_job_object_fn)(int64_t, uint32_t);
typedef uint32_t (__msabi *km_get_process_id_fn)(int64_t);
typedef int32_t (__msabi *km_is_process_in_job_fn)(int64_t, int64_t, int32_t *);
typedef int64_t (__msabi *km_create_mutex_fn)(void *, int32_t, const char16_t *);
typedef uint32_t (__msabi *km_wait_single_object_fn)(int64_t, uint32_t);
typedef int32_t (__msabi *km_release_mutex_fn)(int64_t);

static km_create_job_object_fn km_create_job_object;
static km_assign_process_to_job_object_fn km_assign_process_to_job_object;
static km_terminate_job_object_fn km_terminate_job_object;
static km_get_process_id_fn km_get_process_id;
static km_is_process_in_job_fn km_is_process_in_job;
static km_create_mutex_fn km_create_mutex;
static km_wait_single_object_fn km_wait_single_object;
static km_release_mutex_fn km_release_mutex;
static bool km_windows_job_api_loaded;

static bool km_windows_jobs_needed(void) {
    bool windows = IsWindows();
    intptr_t kernel = windows ? GetModuleHandle("kernel32.dll") : 0;
    return windows && kernel != 0;
}

// Pipeline edge descriptors are only inherited by child processes; Kame does
// not poll them. Use synchronous Win32 pipe handles here so non-Cosmopolitan
// children can safely use them as ordinary standard streams. The captured
// stdout/stderr pipes remain Cosmopolitan's overlapped pipes for nonblocking
// reads in the host event loop.
static int km_windows_sync_pipe(int pipefd[2]) {
    int64_t read_handle = -1, write_handle = -1;
    int reader = -1, writer = -1;
    if (!CreatePipe(&read_handle, &write_handle, NULL, 65536)) { errno = EIO; return -1; }
    __fds_lock();
    reader = __reservefd_unlocked(-1);
    if (reader >= 0) writer = __reservefd_unlocked(-1);
    __fds_unlock();
    if (reader < 0 || writer < 0) {
        if (reader >= 0) __releasefd(reader);
        if (writer >= 0) __releasefd(writer);
        CloseHandle(read_handle);
        CloseHandle(write_handle);
        errno = EMFILE;
        return -1;
    }
    g_fds.p[reader].kind = kFdFile;
    g_fds.p[reader].flags = O_RDONLY | O_CLOEXEC;
    g_fds.p[reader].mode = 0010444;
    g_fds.p[reader].handle = read_handle;
    g_fds.p[writer].kind = kFdFile;
    g_fds.p[writer].flags = O_WRONLY | O_CLOEXEC;
    g_fds.p[writer].mode = 0010222;
    g_fds.p[writer].handle = write_handle;
    pipefd[0] = reader;
    pipefd[1] = writer;
    return 0;
}

static bool km_load_windows_job_api(void) {
    if (!km_windows_job_api_loaded) {
        intptr_t kernel = GetModuleHandle("kernel32.dll");
        if (kernel) {
            km_create_job_object = (km_create_job_object_fn)GetProcAddress(kernel, "CreateJobObjectW");
            km_assign_process_to_job_object = (km_assign_process_to_job_object_fn)GetProcAddress(kernel, "AssignProcessToJobObject");
            km_terminate_job_object = (km_terminate_job_object_fn)GetProcAddress(kernel, "TerminateJobObject");
            km_get_process_id = (km_get_process_id_fn)GetProcAddress(kernel, "GetProcessId");
            km_is_process_in_job = (km_is_process_in_job_fn)GetProcAddress(kernel, "IsProcessInJob");
        }
        km_windows_job_api_loaded = true;
    }
    return km_create_job_object && km_assign_process_to_job_object && km_terminate_job_object;
}

static int km_windows_cache_lock(int stripe) {
    intptr_t kernel = GetModuleHandle("kernel32.dll");
    if (!kernel) return -1;
    if (!km_create_mutex) km_create_mutex = (km_create_mutex_fn)GetProcAddress(kernel, "CreateMutexW");
    if (!km_wait_single_object) km_wait_single_object = (km_wait_single_object_fn)GetProcAddress(kernel, "WaitForSingleObject");
    if (!km_release_mutex) km_release_mutex = (km_release_mutex_fn)GetProcAddress(kernel, "ReleaseMutex");
    if (!km_create_mutex || !km_wait_single_object || !km_release_mutex) return -1;
    char16_t name[48] = {'L','o','c','a','l','\\','K','a','m','e','C','a','c','h','e','L','o','c','k','_'};
    int digits[3] = {stripe / 100, (stripe / 10) % 10, stripe % 10};
    int at = 20;
    if (digits[0]) name[at++] = (char16_t)('0' + digits[0]);
    if (digits[0] || digits[1]) name[at++] = (char16_t)('0' + digits[1]);
    name[at++] = (char16_t)('0' + digits[2]);
    name[at] = 0;
    int64_t mutex = km_create_mutex(NULL, 0, name);
    if (!mutex || mutex == -1) return -1;
    uint32_t result = km_wait_single_object(mutex, 0);
    bool waiting = result == 258;
    int64_t started = km_now();
    // Windows mutexes expose no owner PID. Name the actual mutex, not a fake file.
    char display[64];
    snprintf(display, sizeof(display), "Local\\KameCacheLock_%d", stripe);
    so_String display_name = {.ptr = display, .len = (so_int)strlen(display)};
    while (result == 258) {
        if (!km_lock_notice(-1, display_name, (so_String){"cache", 5}, "waiting", km_now() - started)) {
            CloseHandle(mutex); return -4;
        }
        result = km_wait_single_object(mutex, 100);
    }
    if (result != 0 && result != 0x80) { CloseHandle(mutex); return -1; }
    if (km_cli_signal_pending()) { km_release_mutex(mutex); CloseHandle(mutex); return -4; }
    if (waiting) km_lock_notice(-1, display_name, (so_String){"cache", 5}, "acquired", km_now() - started);
    km_cache_lock_mutex[stripe] = mutex;
    return 0;
}

static void km_windows_cache_unlock(int stripe) {
    int64_t mutex = km_cache_lock_mutex[stripe];
    km_cache_lock_mutex[stripe] = 0;
    if (mutex) {
        if (km_release_mutex) km_release_mutex(mutex);
        CloseHandle(mutex);
    }
}

static int64_t km_windows_job_create(void) {
    if (!km_windows_jobs_needed()) return 0;
    if (!km_load_windows_job_api()) return -1;
    int64_t job = km_create_job_object(NULL, NULL);
    return job && job != -1 ? job : -1;
}

static bool km_windows_job_assign(int64_t job, pid_t pid) {
    if (!km_windows_jobs_needed()) return true;
    if (!job || !km_load_windows_job_api()) return false;
    int64_t process = OpenProcess(0x1000u | 0x0100u | 0x0001u, 0, (uint32_t)pid);
    if (!process || process == -1) return false;
    bool assigned = km_assign_process_to_job_object(job, process) != 0;
    CloseHandle(process);
    return assigned;
}

// Cosmopolitan's Windows execve replaces its tracked process handle with a
// separately created native process. Bind that replacement to the job before
// publishing the started event, so timeout cleanup reaches the requested exe.
// This is not launch-time containment: 4.0.2 creates the replacement using
// Kame as its parent, not the ready-blocked fork child's assigned job. Early
// descendants can therefore escape before the replacement is assigned.
static bool km_windows_job_assign_exec(int64_t job, pid_t pid) {
    // IsWindows is compile-time false on aarch64; keep this guard direct so
    // even unoptimized builds omit the x86-only __proc_search reference.
    if (!IsWindows() || !km_windows_jobs_needed()) return true;
    if (!job || !km_load_windows_job_api() || !km_get_process_id) return false;
    int64_t process = 0;
    uint32_t native_pid = 0;
    for (int attempt = 0; attempt < 500; attempt++) {
        int64_t tracked = __proc_search((int)pid);
        if (!tracked) return true; // The executable already exited and was reaped.
        if (!DuplicateHandle(-1, tracked, -1, &process, 0, 0, 2)) {
            poll(NULL, 0, 1);
            continue;
        }
        native_pid = km_get_process_id(process);
        if (native_pid && native_pid != (uint32_t)pid) {
            bool assigned = km_assign_process_to_job_object(job, process) != 0;
            int32_t in_job = 0;
            bool verified = km_is_process_in_job && km_is_process_in_job(process, job, &in_job) && in_job;
            CloseHandle(process);
            return assigned && verified;
        }
        CloseHandle(process);
        poll(NULL, 0, 1);
    }
    return false;
}

static bool km_windows_job_terminate(int64_t job, int sig) {
    if (!job || !km_windows_jobs_needed()) return true;
    if (!km_load_windows_job_api()) return false;
    bool terminated = km_terminate_job_object(job, (uint32_t)(128 + sig)) != 0;
    return terminated;
}

static void km_windows_job_close(int64_t *job) {
    if (job && *job) CloseHandle(*job);
    if (job) *job = 0;
}
#else
static bool km_windows_jobs_needed(void) { return false; }
static int km_windows_sync_pipe(int pipefd[2]) { (void)pipefd; errno = ENOSYS; return -1; }
static int64_t km_windows_job_create(void) { return 0; }
static bool km_windows_job_assign(int64_t job, pid_t pid) { (void)job; (void)pid; return true; }
static bool km_windows_job_assign_exec(int64_t job, pid_t pid) { (void)job; (void)pid; return true; }
static int km_windows_cache_lock(int stripe) { (void)stripe; return -1; }
static void km_windows_cache_unlock(int stripe) { (void)stripe; }
static bool km_windows_job_terminate(int64_t job, int sig) { (void)job; (void)sig; return true; }
static void km_windows_job_close(int64_t *job) { if (job) *job = 0; }
#endif

static volatile sig_atomic_t km_cli_signal = 0;
static volatile sig_atomic_t km_cli_signal_count = 0;
static volatile sig_atomic_t km_cli_first_signal_taken = 0;
extern char **environ;

static void km_cli_signal_handler(int signal_number) {
    km_cli_signal = signal_number;
    if (km_cli_signal_count < 2) km_cli_signal_count++;
}

int km_cli_install_signals(void) {
    struct sigaction action;
    memset(&action, 0, sizeof(action));
    action.sa_handler = km_cli_signal_handler;
    sigemptyset(&action.sa_mask);
    action.sa_flags = 0;
    km_cli_signal = 0;
    km_cli_signal_count = 0;
    km_cli_first_signal_taken = 0;
    if (sigaction(SIGINT, &action, NULL) != 0) return -1;
    if (sigaction(SIGTERM, &action, NULL) != 0) return -1;
    return 0;
}

int km_cli_take_signal(void) {
    if (km_cli_signal_count == 0) return 0;
    if (!km_cli_first_signal_taken) {
        km_cli_first_signal_taken = 1;
        km_cli_signal_count--;
        return (int)km_cli_signal;
    }
    km_cli_signal_count = 0;
    return -(int)km_cli_signal;
}

bool km_cli_signal_pending(void) { return km_cli_signal_count != 0; }

int km_cli_stderr_is_terminal(void) { return isatty(STDERR_FILENO); }
int km_cli_stdout_is_terminal(void) { return isatty(STDOUT_FILENO); }

int km_cli_stderr_height(void) {
    struct winsize size;
    if (ioctl(STDERR_FILENO, TIOCGWINSZ, &size) != 0) return 0;
    return (int)size.ws_row;
}

int km_cli_stderr_width(void) {
    struct winsize size;
    if (ioctl(STDERR_FILENO, TIOCGWINSZ, &size) != 0 || size.ws_col == 0) return 0;
    return (int)size.ws_col;
}

int km_cli_environment_size(void) {
    size_t total = 0;
    if (environ == NULL) return 0;
    for (char **entry = environ; *entry != NULL; entry++) total += strlen(*entry) + 1;
    if (total > INT_MAX) return -1;
    return (int)total;
}

int km_cli_environment_copy(so_Slice out) {
    int required = km_cli_environment_size();
    if (required < 0 || out.len < required) return -1;
    int offset = 0;
    if (environ == NULL) return 0;
    for (char **entry = environ; *entry != NULL; entry++) {
        size_t length = strlen(*entry) + 1;
        memcpy(out.ptr + offset, *entry, length);
        offset += (int)length;
    }
    return offset;
}

bool km_cli_environment_names_case_insensitive(void) {
#if defined(__COSMOPOLITAN__)
    return IsWindows();
#else
    return false;
#endif
}

// The recipe host calls setpgid and captures every stdio stream, so an
// interactive filter would not receive terminal signals. This helper leaves
// the child in the foreground group, feeds the list on stdin, and returns
// the selection from stdout. The filter opens /dev/tty for its UI.
static void km_close(int *fd);
static char *km_cstring(so_String value);
static void km_free_strings(char **strings, int count);
static int km_pick_write_fd(int fd, const char *data, int len) {
    int written = 0;
    while (written < len) {
        ssize_t n = write(fd, data + written, (size_t)(len - written));
        if (n > 0) { written += (int)n; continue; }
        if (n < 0 && errno == EINTR) continue;
        return -1;
    }
    return 0;
}

static int km_pick_read(int fd, so_Slice selection) {
    if (selection.len <= 0 || !selection.ptr) return -1;
    int got = 0, overflow = 0;
    char discard[4096];
    for (;;) {
        ssize_t n = got < selection.len
            ? read(fd, (char *)selection.ptr + got, (size_t)(selection.len - got))
            : read(fd, discard, sizeof(discard));
        if (n > 0) {
            if (got < selection.len) got += (int)n;
            else overflow = 1;
            continue;
        }
        if (n == 0) return overflow ? -1 : got;
        if (errno == EINTR) continue;
        return -1;
    }
}

static int km_pick_wait(pid_t pid) {
    int status = 0;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno != EINTR) return -1;
    }
    if (WIFEXITED(status)) return WEXITSTATUS(status);
    if (WIFSIGNALED(status)) {
        if (WTERMSIG(status) == SIGINT) return 130;
        return 128 + WTERMSIG(status);
    }
    return -1;
}

int km_pick_select(so_String executable, so_Slice argv, so_String input, so_Slice selection, so_int *selected) {
    int tty = -1, file = -1, argc = 0, built = 0, got = 0, line = 0, result = -1;
    int out[2] = {-1, -1};
    pid_t pid = -1;
    char **child_argv = NULL;
    so_String *items = NULL;
    char path[] = "/tmp/kame-pick-XXXXXX";
    char *bytes = NULL;
    if (km_cli_environment_names_case_insensitive()) return -2;
    if (!selected || executable.len <= 0 || !executable.ptr || argv.len < 0 || input.len < 0 || (input.len && !input.ptr)) return -1;
    *selected = 0;
    tty = open("/dev/tty", O_RDWR | O_CLOEXEC);
    if (tty < 0) return -3;
    close(tty);
    argc = (int)argv.len;
    child_argv = calloc((size_t)argc + 2, sizeof(char *));
    if (!child_argv) return -1;
    child_argv[0] = km_cstring(executable);
    items = argv.len ? argv.ptr : NULL;
    built = child_argv[0] != NULL;
    for (int i = 0; built && i < argc; i++) {
        child_argv[i + 1] = km_cstring(items[i]);
        if (!child_argv[i + 1]) built = 0;
    }
    if (!built) { km_free_strings(child_argv, argc + 1); return -1; }
    file = mkstemp(path);
    if (file < 0) goto done;
    unlink(path);
    if (km_pick_write_fd(file, input.ptr, (int)input.len) != 0 || lseek(file, 0, SEEK_SET) < 0 || pipe(out) != 0) goto done;
    pid = fork();
    if (pid < 0) goto done;
    if (pid == 0) {
        if (dup2(file, STDIN_FILENO) < 0 || dup2(out[1], STDOUT_FILENO) < 0) _exit(127);
        if (file != STDIN_FILENO) close(file);
        close(out[0]);
        if (out[1] != STDOUT_FILENO) close(out[1]);
        execvp(child_argv[0], child_argv);
        _exit(127);
    }
    km_close(&file);
    km_close(&out[1]);
    got = km_pick_read(out[0], selection);
    result = km_pick_wait(pid);
    pid = -1;
    if (got < 0 || result < 0) { result = -1; goto done; }
    if (result == 0) {
        bytes = selection.ptr;
        while (line < got && bytes[line] != '\n' && bytes[line] != '\r') line++;
        if (line == got && got == selection.len) { result = -1; goto done; }
        if (line == 0) result = 130;
        else *selected = line;
    }
done:
    if (pid > 0) km_pick_wait(pid);
    km_close(&file);
    km_close(&out[0]);
    km_close(&out[1]);
    km_free_strings(child_argv, argc + 1);
    return result;
}

static int64_t km_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static int64_t km_output_now_ns(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) return -1;
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

int km_cli_write_output(int fd, so_Slice data, int64_t timeout_ns, so_int *written) {
    *written = 0;
    if (timeout_ns < 0) return 1;
    bool bounded = timeout_ns != 0;
    int flags = -1, result = 0;
    int64_t deadline = 0;
    if (bounded) {
        int64_t now = km_output_now_ns();
        if (now < 0 || timeout_ns > INT64_MAX - now) return -1;
        deadline = now + timeout_ns;
        flags = fcntl(fd, F_GETFL);
        if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) != 0) return -1;
    }
    while (*written < data.len) {
        if (bounded) {
            int64_t now = km_output_now_ns();
            if (now < 0) { result = -1; break; }
            if (now >= deadline) { result = 1; break; }
        }
        size_t count = (size_t)(data.len - *written);
        if (bounded && count > KM_CHUNK) count = KM_CHUNK;
        ssize_t n = write(fd, (const so_byte *)data.ptr + *written, count);
        if (n > 0) { *written += n; continue; }
        if (n < 0 && errno == EINTR) {
            if (!bounded && km_cli_signal_count != 0) { result = -1; break; }
            continue;
        }
        if (bounded && n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            int64_t now = km_output_now_ns();
            if (now < 0) { result = -1; break; }
            int64_t remaining = deadline - now;
            if (remaining <= 0) { result = 1; break; }
            int64_t wait = remaining / 1000000 + (remaining % 1000000 != 0);
            if (wait > INT_MAX) wait = INT_MAX;
            struct pollfd pollfd = {.fd = fd, .events = POLLOUT};
            int ready = poll(&pollfd, 1, (int)wait);
            if (ready < 0 && errno != EINTR) { result = -1; break; }
            continue;
        }
        result = -1;
        break;
    }
    if (bounded && fcntl(fd, F_SETFL, flags) != 0 && result == 0) result = -1;
    return result;
}

// Native test probe: fail all event allocations after N successful allocations.
// It intentionally excludes process setup and the So allocator.
static int km_event_allocations_left = -1;
void km_event_force_allocation_failure(int after) { km_event_allocations_left = after; }
static bool km_event_allocate(void) {
    if (km_event_allocations_left < 0) return true;
    if (!km_event_allocations_left) return false;
    km_event_allocations_left--; return true;
}
static const char km_allocation_code[] = KM_HOSTF;
static const char km_allocation_diagnostic[] = "event allocation failed";

static void km_free_event(km_event *event) {
    free(event->data);
    if (event->diagnosticCode != (const so_byte *)km_allocation_code) free(event->diagnosticCode);
    if (event->diagnostic != (const so_byte *)km_allocation_diagnostic) free(event->diagnostic);
    free(event->output); free(event->errorOutput);
    free(event->stageData);
    free(event->metricData);
    memset(event, 0, sizeof(*event));
}

void km_event_free(km_event *event) { km_free_event(event); }

static void km_fail(km_host *, km_process *, const char *);

static void km_allocation_failed(km_event *event) {
    if (event->diagnosticCode != (const so_byte *)km_allocation_code) free(event->diagnosticCode);
    if (event->diagnostic != (const so_byte *)km_allocation_diagnostic) free(event->diagnostic);
    event->diagnosticCode = (so_byte *)km_allocation_code;
    event->diagnosticCodeLen = sizeof(km_allocation_code) - 1;
    event->diagnostic = (so_byte *)km_allocation_diagnostic;
    event->diagnosticLen = sizeof(km_allocation_diagnostic) - 1;
    event->outcome = KM_FAILED;
}

static bool km_push(km_host *host, km_process *process, km_event event) {
    // Snapshots are advisory and coalesced, never an unbounded history or a
    // reason to fail work. Keep lifecycle events in their existing order.
    if (event.kind == KM_SNAPSHOT) {
        for (km_event_node *old = host->first; old; old = old->next) {
            if (old->event.kind == KM_SNAPSHOT && old->event.id == event.id) {
                km_free_event(&old->event); old->event = event; return true;
            }
        }
    }
    bool chunk = event.kind == KM_STDOUT || event.kind == KM_STDERR;
    if (chunk && event.dataLen && !event.data) {
        km_free_event(&event); km_fail(host, process, "output queue allocation failed"); return false;
    }
    if (event.kind == KM_TERMINAL &&
        ((event.stdoutLen && !event.output) || (event.stderrLen && !event.errorOutput) ||
         (event.diagnosticCodeLen && !event.diagnosticCode) || (event.diagnosticLen && !event.diagnostic))) {
        if (!event.output) { event.stdoutLen = 0; event.stdoutTruncated = true; }
        if (!event.errorOutput) { event.stderrLen = 0; event.stderrTruncated = true; }
        km_allocation_failed(&event);
    }
    km_event_node *node = km_event_allocate() ? calloc(1, sizeof(*node)) : NULL;
    if (!node) {
        if (event.kind == KM_SNAPSHOT) { km_free_event(&event); return false; }
        if (chunk) { km_free_event(&event); km_fail(host, process, "output queue allocation failed"); return false; }
        uint64_t sequence = ++host->sequence;
        if (process) {
            int slot = event.kind == KM_STARTED ? 0 : 1;
            process->pending[slot] = event; process->pending_sequence[slot] = sequence;
        } else {
            host->rejected = event; host->rejected_sequence = sequence;
        }
        return true;
    }
    node->event = event; node->process_id = process ? process->id : 0;
    node->sequence = ++host->sequence;
    if (host->last) host->last->next = node; else host->first = node;
    host->last = node;
    if (chunk && process) process->queued += event.dataLen;
    return true;
}

static so_byte *km_copy(const void *data, int len) {
    if (len <= 0) return NULL;
    so_byte *out = km_event_allocate() ? malloc((size_t)len) : NULL;
    if (out) memcpy(out, data, (size_t)len);
    return out;
}

static char *km_cstring(so_String value) {
    if (value.len < 0 || (value.len && (!value.ptr || memchr(value.ptr, '\0', (size_t)value.len)))) return NULL;
    char *out = calloc((size_t)value.len + 1, 1);
    if (out && value.len) memcpy(out, value.ptr, (size_t)value.len);
    return out;
}

// Real storage sync, not only stdio fflush. Return errno (zero on success).
int km_file_sync(void *stream) {
    if (!stream) return EINVAL;
    if (fflush(stream) != 0) return errno ? errno : EIO;
    int result;
    do { result = fsync(fileno(stream)); } while (result < 0 && errno == EINTR);
    return result == 0 ? 0 : (errno ? errno : EIO);
}

int km_file_sync_parent(so_String directory) {
#if defined(__COSMOPOLITAN__)
    // No verified directory-flush seam on Windows. Do not promise durability.
    if (IsWindows()) return ENOTSUP;
#endif
    char *path = km_cstring(directory);
    if (!path) return ENOMEM;
    int fd = open(path, O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    free(path);
    if (fd < 0) return errno ? errno : EIO;
    int result;
    do { result = fsync(fd); } while (result < 0 && errno == EINTR);
    int error = result == 0 ? 0 : (errno ? errno : EIO);
    if (close(fd) != 0 && !error) error = errno ? errno : EIO;
    return error;
}

int64_t km_file_modtime(so_String name, bool link) {
    char *path = km_cstring(name);
    if (!path) return 0;
    struct stat info;
    int result = link ? lstat(path, &info) : stat(path, &info);
    free(path);
    if (result != 0) return 0;
#if defined(__APPLE__)
    return (int64_t)info.st_mtimespec.tv_sec * 1000000000LL + info.st_mtimespec.tv_nsec;
#else
    return (int64_t)info.st_mtim.tv_sec * 1000000000LL + info.st_mtim.tv_nsec;
#endif
}

// A polling hint, not a content identity. Do not include atime: our own reads
// must not invalidate it. Following and no-follow views cover symlink changes.
// Two 49-byte views: presence plus six little-endian uint64 fields.
bool km_file_watch_stamp(so_String name, so_Slice data) {
#if defined(__COSMOPOLITAN__)
    if (IsWindows()) return false; // Windows ctime is not a change timestamp.
#endif
    if (data.len != 98) return false;
    char *path = km_cstring(name);
    if (!path) return false;
    unsigned char *out = data.ptr;
    memset(out, 0, 98);
    for (int link = 0; link < 2; ++link) {
        struct stat info;
        int result = link ? lstat(path, &info) : stat(path, &info);
        if (result != 0) {
            if (errno == ENOENT || errno == ENOTDIR) continue;
            free(path); return false;
        }
#if defined(__APPLE__)
        int64_t mtime = (int64_t)info.st_mtimespec.tv_sec * 1000000000LL + info.st_mtimespec.tv_nsec;
        int64_t ctime = (int64_t)info.st_ctimespec.tv_sec * 1000000000LL + info.st_ctimespec.tv_nsec;
#else
        int64_t mtime = (int64_t)info.st_mtim.tv_sec * 1000000000LL + info.st_mtim.tv_nsec;
        int64_t ctime = (int64_t)info.st_ctim.tv_sec * 1000000000LL + info.st_ctim.tv_nsec;
#endif
        uint64_t fields[] = {info.st_dev, info.st_ino, info.st_mode, info.st_size, mtime, ctime};
        out[link * 49] = 1;
        for (int i = 0; i < 6; ++i)
            for (int j = 0; j < 8; ++j)
                out[link * 49 + 1 + i * 8 + j] = fields[i] >> (j * 8);
    }
    free(path); return true;
}

// Notifications are hints only. Replace subscriptions before fingerprinting,
// retaining events on the new descriptor during the scan.
struct km_watch { int fd, next; bool complete; };

km_watch *km_watch_new(void) {
    km_watch *w = calloc(1, sizeof(*w));
    if (w) w->fd = w->next = -1;
    return w;
}

void km_watch_begin(km_watch *w) {
    if (!w) return;
    if (w->next >= 0) close(w->next);
    w->next = -1;
#if defined(__linux__) || defined(__COSMOPOLITAN__)
#if defined(__COSMOPOLITAN__)
    if (IsLinux())
#endif
    w->next = inotify_init1(IN_NONBLOCK | IN_CLOEXEC);
#endif
    w->complete = w->next >= 0;
}

static bool km_watch_directory(km_watch *w, const char *path, bool recursive, int depth) {
#if defined(__linux__) || defined(__COSMOPOLITAN__)
    if (w->next < 0 || depth > 128) return false;
    if (inotify_add_watch(w->next, path, IN_MODIFY | IN_ATTRIB | IN_CREATE | IN_DELETE |
                         IN_MOVED_FROM | IN_MOVED_TO | IN_DELETE_SELF | IN_MOVE_SELF) < 0) return false;
    if (!recursive) return true;
    // ponytail: subscribe the whole glob-root subtree; prune by pattern if watch
    // limits or traversal cost become material. Failed coverage enables polling.
    DIR *dir = opendir(path);
    if (!dir) return false;
    bool ok = true;
    struct dirent *entry;
    while ((entry = readdir(dir))) {
        if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, "..")) continue;
        size_t size = strlen(path) + strlen(entry->d_name) + 2;
        char *child = malloc(size);
        if (!child) { ok = false; break; }
        snprintf(child, size, "%s/%s", path, entry->d_name);
        struct stat info;
        if (lstat(child, &info) != 0) ok = false;
        else if (S_ISDIR(info.st_mode)) ok = km_watch_directory(w, child, true, depth + 1) && ok;
        free(child);
    }
    closedir(dir);
    return ok;
#else
    (void)w; (void)path; (void)recursive; (void)depth;
    return false;
#endif
}

static void km_watch_parent(char *path) {
    char *slash = strrchr(path, '/');
    if (!slash) strcpy(path, ".");
    else if (slash == path) path[1] = 0;
    else *slash = 0;
}

static bool km_watch_existing(km_watch *w, char *path, bool recursive) {
    struct stat info;
    while (stat(path, &info) != 0 || !S_ISDIR(info.st_mode)) {
        if (!strcmp(path, "/") || !strcmp(path, ".")) return false;
        km_watch_parent(path);
        // A missing subtree only needs its nearest existing ancestor.
        recursive = false;
    }
    return km_watch_directory(w, path, recursive, 0);
}

bool km_watch_add(km_watch *w, so_String name, bool glob) {
    if (!w || w->next < 0) return false;
    if (name.len <= 0) { w->complete = false; return false; }
    char *path = km_cstring(name);
    if (!path) { w->complete = false; return false; }
    bool ok = true;
    if (glob) {
        char *meta = strpbrk(path, "*?[\\");
        if (meta) *meta = 0;
        km_watch_parent(path);
        ok = km_watch_existing(w, path, true);
    } else {
        // Follow existing symlinks to cover target edits outside the link's parent.
        char *target = realpath(path, NULL);
        if (target) {
            km_watch_parent(target);
            ok = km_watch_existing(w, target, false);
            free(target);
        }
        // A dangling symlink may point outside the link's directory, too.
        char link[PATH_MAX + 1];
        ssize_t length = readlink(path, link, PATH_MAX);
        if (length >= 0) {
            link[length] = 0;
            char *base = strdup(path);
            if (!base) ok = false;
            else {
                km_watch_parent(base);
                size_t size = strlen(base) + (size_t)length + 2;
                char *destination = malloc(size);
                if (!destination) ok = false;
                else {
                    if (link[0] == '/') snprintf(destination, size, "%s", link);
                    else snprintf(destination, size, "%s/%s", base, link);
                    km_watch_parent(destination);
                    ok = km_watch_existing(w, destination, false) && ok;
                    free(destination);
                }
                free(base);
            }
        }
        km_watch_parent(path);
        ok = km_watch_existing(w, path, false) && ok;
    }
    free(path);
    w->complete = w->complete && ok;
    return ok;
}

bool km_watch_commit(km_watch *w) {
    if (!w) return false;
    if (w->fd >= 0) close(w->fd);
    w->fd = w->next; w->next = -1;
    return w->complete;
}

int km_watch_wait(km_watch *w, int wait) {
    struct pollfd fd = { .fd = w ? w->fd : -1, .events = POLLIN };
    int result = poll(&fd, 1, wait);
    if (result < 0) return errno == EINTR ? 0 : -1;
    if (!result) return 0;
    if (fd.revents & (POLLERR | POLLHUP | POLLNVAL)) return -1;
    // Drain the entire queue; overflow and lost subscriptions also cause rescan.
    char data[16384];
    while (read(fd.fd, data, sizeof(data)) > 0) {}
    return 1;
}

void km_watch_free(km_watch *w) {
    if (!w) return;
    if (w->fd >= 0) close(w->fd);
    if (w->next >= 0) close(w->next);
    free(w);
}

static void km_free_strings(char **strings, int count) {
    if (!strings) return;
    for (int i = 0; i < count; i++) free(strings[i]);
    free(strings);
}

static void km_diagnostic(km_event *event, const char *code, const char *text) {
    int cn = (int)strlen(code);
    event->diagnosticCode = km_copy(code, cn); event->diagnosticCodeLen = cn;
    int n = (int)strlen(text);
    event->diagnostic = km_copy(text, n); event->diagnosticLen = n;
}

static void km_close(int *fd) { if (*fd >= 0) { close(*fd); *fd = -1; } }

static bool km_close_checked(int *fd) {
    if (*fd < 0) return true;
    int saved = close(*fd); *fd = -1;
    return saved == 0;
}

static void km_release_process(km_process *p) {
    km_close(&p->outfd); km_close(&p->errfd); km_close(&p->execfd);
    free(p->stdout_data); free(p->stderr_data);
    km_windows_job_close(&p->job);
    for (int i = 0; i < p->stage_count; i++) km_windows_job_close(&p->stages[i].job);
    free(p->stages);
    if (p->metrics) munmap(p->metrics, (size_t)p->metric_count * KM_METRIC_FIELDS * sizeof(int64_t));
    if (p->live_states) munmap(p->live_states, (size_t)p->stage_count * 2 * sizeof(int));
    for (int i = 0; i < 2; i++) if (p->pending_sequence[i]) km_free_event(&p->pending[i]);
    memset(p, 0, sizeof(*p)); p->outfd = p->errfd = p->execfd = -1;
}

static bool km_append(so_byte **data, int *len, int *cap, const so_byte *chunk, int n, int limit, bool *truncated) {
    if (*len >= limit) { *truncated = true; return true; }
    if (n > limit - *len) { n = limit - *len; *truncated = true; }
    if (*len + n > *cap) {
        int next = *cap ? *cap : 64;
        while (next < *len + n) next *= 2;
        so_byte *grown = km_event_allocate() ? realloc(*data, (size_t)next) : NULL;
        if (!grown) return false;
        *data = grown; *cap = next;
    }
    memcpy(*data + *len, chunk, (size_t)n); *len += n;
    return true;
}

static void km_emit_terminal(km_host *host, km_process *p, int outcome, const char *diagnostic) {
    if (p->terminal) return;
    p->terminal = true;
    km_event event = { .kind = KM_TERMINAL, .id = p->id, .pid = p->pid, .pgid = p->pid, .outcome = outcome, .status = p->status, .signal = p->signal, .output = km_copy(p->stdout_data, p->stdout_len), .stdoutLen = p->stdout_len, .errorOutput = km_copy(p->stderr_data, p->stderr_len), .stderrLen = p->stderr_len, .stdoutTruncated = p->stdout_truncated, .stderrTruncated = p->stderr_truncated, .retainBytes = p->retain };
    if (p->stage_count) {
        event.stageData = km_event_allocate() ? calloc((size_t)p->stage_count * 3, sizeof(int)) : NULL;
        if (!event.stageData) { outcome = event.outcome = KM_FAILED; diagnostic = "stage result allocation failed"; }
        else {
            event.stageCount = p->stage_count;
            for (int i = 0; i < p->stage_count; i++) {
                event.stageData[i * 3] = p->stages[i].status;
                event.stageData[i * 3 + 1] = p->stages[i].signal;
                event.stageData[i * 3 + 2] = p->stages[i].outcome;
            }
        }
    }
    // Telemetry is optional: an allocation failure must not turn successful
    // work into a failed graph. Missing measurements remain explicitly unknown.
    if (p->metrics && p->exited) {
        size_t size = (size_t)p->metric_count * KM_METRIC_FIELDS * sizeof(int64_t);
        event.metricData = km_event_allocate() ? malloc(size) : NULL;
        if (event.metricData) { memcpy(event.metricData, p->metrics, size); event.metricCount = p->metric_count; }
    }
    if (diagnostic) km_diagnostic(&event, KM_HOSTF, diagnostic);
    km_push(host, p, event);
}

// Each stage owns a process group, including its descendants. Graph cancellation
// signals every group, even if its immediate stage process has already exited.
static int km_signal(km_process *p, int sig) {
    int failed = 0;
    if (!p->stage_count) {
        if (km_windows_jobs_needed() && p->job) {
            if (sig == SIGKILL) return km_windows_job_terminate(p->job, sig) ? 0 : -1;
            if (p->reaped) return 0; // The numeric Windows PID may have been reused.
            return kill(p->pid, sig) < 0 && errno != ESRCH ? -1 : 0;
        }
        if (p->reaped || p->pid <= 0) return 0;
        return kill(-p->pid, sig) < 0 && errno != ESRCH ? -1 : 0;
    }
    for (int i = 0; i < p->stage_count; i++) if (p->stages[i].pid > 0) {
        if (km_windows_jobs_needed() && p->stages[i].job) {
            if (sig == SIGKILL) { if (!km_windows_job_terminate(p->stages[i].job, sig)) failed = -1; }
            else if (!p->stages[i].reaped && kill(p->stages[i].pid, sig) < 0 && errno != ESRCH) failed = -1;
        } else if (!p->stages[i].reaped && kill(-p->stages[i].pid, sig) < 0 && errno != ESRCH) failed = -1;
    }
    return failed;
}

static bool km_wait_child(pid_t pid, bool *reaped, int *code, int *signal_number) {
    if (*reaped || pid <= 0) return true;
    int status; pid_t result;
    do { result = waitpid(pid, &status, 0); } while (result < 0 && errno == EINTR);
    if (result != pid) {
        // ECHILD means we no longer own this PID; never signal it again. Other
        // failures leave ownership tracked for another cleanup attempt.
        if (errno == ECHILD) *reaped = true;
        return false;
    }
    *reaped = true;
    if (WIFEXITED(status)) *code = WEXITSTATUS(status);
    else if (WIFSIGNALED(status)) *signal_number = WTERMSIG(status);
    return true;
}

static bool km_wait_graph(km_process *p) {
    bool all = true, ok = true;
    for (int i = 0; i < p->stage_count; i++) {
        km_stage *s = &p->stages[i];
        if (!km_wait_child(s->pid, &s->reaped, &s->status, &s->signal)) ok = false;
        if (s->pid > 0 && !s->reaped) all = false;
    }
    p->reaped = all;
    return ok;
}

static void km_fail(km_host *host, km_process *p, const char *diagnostic) {
    if (p->terminal) return;
    km_signal(p, SIGKILL);
    if (p->stage_count) {
        for (int i = 0; i < p->stage_count; i++) if (!p->stages[i].reaped && !p->terminating) p->stages[i].outcome = KM_FAILED;
        km_wait_graph(p);
    } else km_wait_child(p->pid, &p->reaped, &p->status, &p->signal);
    km_close(&p->outfd); km_close(&p->errfd);
    km_emit_terminal(host, p, KM_FAILED, diagnostic);
}

static km_process *km_process_for(km_host *host, int64_t id) {
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return &host->processes[i];
    return NULL;
}

static void km_remove_process(km_host *host, int64_t id) {
    for (int i = 0; i < host->len; i++) {
        if (host->processes[i].id != id) continue;
        if (!host->processes[i].reaped) return;
        km_release_process(&host->processes[i]);
        host->len--;
        if (i != host->len) host->processes[i] = host->processes[host->len];
        return;
    }
}

static int km_set_nonblock(int fd) {
    int flags = fcntl(fd, F_GETFL);
    return flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) < 0 ? -1 : 0;
}

km_host *km_host_new(void) {
    km_host *host = calloc(1, sizeof(km_host));
    if (!host) return NULL;
    if (!km_cache_lock_initialized) {
        for (int i = 0; i < 256; i++) km_cache_lock_fd[i] = -1;
        km_cache_lock_initialized = true;
    }
    return host;
}

static bool (*km_lock_observer)(km_lock_event *);
static char km_lock_command[128] = "kame";

void km_cli_observe_locks(bool (*observer)(km_lock_event *), so_String command) {
    km_lock_observer = observer;
    size_t n = command.len < 127 ? command.len : 127;
    for (size_t i = 0; i < n; i++) {
        unsigned char ch = command.ptr[i];
        km_lock_command[i] = ch < 32 || ch == 127 ? '?' : ch;
    }
    km_lock_command[n] = 0;
}

// Metadata is advisory and never authorizes lock removal or acquisition. Linux
// kernel records verify the current holder, including non-Kame flock users.
static long km_lock_kernel_owner(int fd) {
#if defined(__linux__) || defined(__COSMOPOLITAN__)
#if defined(__COSMOPOLITAN__)
    if (!IsLinux()) return 0;
#endif
    struct stat st;
    if (fstat(fd, &st)) return 0;
    FILE *locks = fopen("/proc/locks", "r");
    if (!locks) return 0;
    char line[512], type[16], mode[16], access[16];
    long pid = 0, candidate;
    unsigned device_major, device_minor;
    unsigned long long inode;
    while (fgets(line, sizeof(line), locks)) {
        if (sscanf(line, "%*u: %15s %15s %15s %ld %x:%x:%llu", type, mode, access,
                   &candidate, &device_major, &device_minor, &inode) == 7 &&
            !strcmp(type, "FLOCK") && !strcmp(access, "WRITE") && candidate > 0 &&
            device_major == major(st.st_dev) && device_minor == minor(st.st_dev) && inode == st.st_ino) {
            pid = candidate; break;
        }
    }
    fclose(locks);
    return pid;
#else
    (void)fd; return 0;
#endif
}

static unsigned long long km_lock_process_start(long pid) {
    char path[64], data[1024];
    snprintf(path, sizeof(path), "/proc/%ld/stat", pid);
    int fd = open(path, O_RDONLY | O_CLOEXEC);
    if (fd < 0) return 0;
    ssize_t n = read(fd, data, sizeof(data) - 1);
    close(fd);
    if (n <= 0) return 0;
    data[n] = 0;
    char *p = strrchr(data, ')');
    if (!p) return 0;
    p++;
    for (int field = 3; field < 22; field++) {
        while (*p == ' ') p++;
        while (*p && *p != ' ') p++;
    }
    return strtoull(p, NULL, 10);
}

static int64_t km_lock_wall_ms(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now)) return 0;
    return (int64_t)now.tv_sec * 1000 + now.tv_nsec / 1000000;
}

static bool km_lock_notice(int fd, so_String path, so_String kind, const char *state, int64_t elapsed) {
    if (!km_lock_observer) return !km_cli_signal_pending();
    char data[320] = {0}, command[128] = {0};
    long recorded = 0, pid = fd < 0 ? 0 : km_lock_kernel_owner(fd);
    unsigned long long birth = 0;
    long long since = 0;
    bool verified = pid > 0;
    int64_t held = -1;
    ssize_t n = fd < 0 ? -1 : pread(fd, data, sizeof(data) - 1, 0);
    if (n > 0) {
        data[n] = 0;
        char *summary = strchr(data, '\n');
        // Cosmopolitan sscanf does not support scansets; copy the summary below.
        if (sscanf(data, "kame-lock-v1 %ld %llu %lld", &recorded, &birth, &since) == 3 && summary &&
            recorded > 0 && recorded <= INT_MAX && (!birth || birth == km_lock_process_start(recorded)) &&
            (pid == recorded || (!pid && (!kill((pid_t)recorded, 0) || errno == EPERM)))) {
            pid = recorded;
            size_t length = strcspn(summary + 1, "\n");
            if (length >= sizeof(command)) length = sizeof(command) - 1;
            memcpy(command, summary + 1, length);
            if (since > 0 && since <= km_lock_wall_ms()) held = km_lock_wall_ms() - since;
        }
    }
    if (pid && !command[0]) {
        char name[64];
        snprintf(name, sizeof(name), "/proc/%ld/comm", pid);
        int process = open(name, O_RDONLY | O_CLOEXEC);
        if (process >= 0) {
            n = read(process, command, sizeof(command) - 1);
            close(process);
            if (n > 0) command[n] = 0;
        }
    }
    for (char *p = command; *p; p++) if ((unsigned char)*p < 32 || *p == 127) *p = ' ';
    km_lock_event event = {.State = {.ptr = state, .len = (so_int)strlen(state)}, .Kind = kind, .Path = path,
        .OwnerPID = pid, .OwnerCommand = {.ptr = command, .len = (so_int)strlen(command)}, .OwnerVerified = verified,
        .ElapsedMS = elapsed, .HeldMS = held};
    return km_lock_observer(&event);
}

// O_NOFOLLOW excludes symlinks, not hard links. Never write diagnostic metadata
// through an alias of a user file, including aliases added during a lock wait.
static bool km_lock_metadata_safe(int fd) {
    struct stat info;
    return !fstat(fd, &info) && S_ISREG(info.st_mode) && info.st_nlink == 1;
}

int km_file_lock(int fd, so_String path, so_String kind, bool wait) {
    if (!km_lock_metadata_safe(fd)) return -1;
    int64_t started = km_now();
    bool waiting = false;
    for (;;) {
        if (km_cli_signal_pending()) return -4;
        if (!flock(fd, LOCK_EX | LOCK_NB)) break;
        if (errno == EINTR) return -4;
        if (errno != EWOULDBLOCK && errno != EAGAIN) return -1;
        if (!wait) { km_lock_notice(fd, path, kind, "busy", 0); return -2; }
        waiting = true;
        if (!km_lock_notice(fd, path, kind, "waiting", km_now() - started)) return -4;
        struct timespec pause = {.tv_nsec = 100000000};
        if (nanosleep(&pause, NULL) && errno == EINTR) return -4;
        if (km_cli_signal_pending()) return -4;
    }
    if (km_cli_signal_pending()) { flock(fd, LOCK_UN); return -4; }
    char data[320];
    int n = snprintf(data, sizeof(data), "kame-lock-v1 %ld %llu %lld\n%s\n", (long)getpid(),
                     km_lock_process_start(getpid()), (long long)km_lock_wall_ms(), km_lock_command);
    // Diagnostics must never turn a successfully acquired lock into a failure.
    if (km_lock_metadata_safe(fd) && !ftruncate(fd, 0)) { ssize_t wrote = pwrite(fd, data, (size_t)n, 0); (void)wrote; }
    if (waiting) km_lock_notice(fd, path, kind, "acquired", km_now() - started);
    return 0;
}

void km_file_unlock(int fd) {
    // Clear while still holding the lock; never unlink/replace the lock inode.
    if (km_lock_metadata_safe(fd)) { int ignored = ftruncate(fd, 0); (void)ignored; }
    flock(fd, LOCK_UN);
}

int km_host_cache_lock(km_host *host, so_String name, int stripe) {
    if (!host || stripe < 0 || stripe >= 256) return -1;
    if (km_cli_signal_pending()) return -4;
    if (km_cache_lock_refs[stripe] != 0) {
        host->cache_lock_refs[stripe]++;
        km_cache_lock_refs[stripe]++;
        return 0;
    }
    if (km_windows_jobs_needed()) {
        int status = km_windows_cache_lock(stripe);
        if (status != 0) return status;
        host->cache_lock_refs[stripe] = 1;
        km_cache_lock_refs[stripe] = 1;
        return 0;
    }
    char *path = km_cstring(name);
    if (!path) return -1;
    int fd = open(path, O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
    free(path);
    if (fd < 0) return -1;
    int status = km_file_lock(fd, name, (so_String){"cache", 5}, true);
    if (status != 0) {
        close(fd);
        return status;
    }
    host->cache_lock_refs[stripe] = 1;
    km_cache_lock_fd[stripe] = fd;
    km_cache_lock_refs[stripe] = 1;
    return 0;
}

void km_host_cache_unlock(km_host *host, int stripe) {
    if (!host || stripe < 0 || stripe >= 256 || host->cache_lock_refs[stripe] == 0 || km_cache_lock_refs[stripe] == 0) return;
    host->cache_lock_refs[stripe]--;
    if (--km_cache_lock_refs[stripe] != 0) return;
    if (km_windows_jobs_needed()) { km_windows_cache_unlock(stripe); return; }
    int fd = km_cache_lock_fd[stripe];
    km_cache_lock_fd[stripe] = -1;
    if (fd >= 0) { km_file_unlock(fd); close(fd); }
}

static int km_spawn_failed(km_host *host, int64_t id, const char *message) {
    km_event event = {.kind = KM_TERMINAL, .id = id, .outcome = KM_FAILED};
    km_diagnostic(&event, KM_HOSTF, message);
    km_push(host, NULL, event);
    return -1;
}

// A post-fork setup failure must not leave an untracked child behind. Retry an
// interrupted wait and let the terminal diagnostic distinguish failed cleanup
// from an ordinary spawn failure.
static bool km_abort_spawn(pid_t pid, int64_t job) {
    if (km_windows_jobs_needed() && job) {
        if (!km_windows_job_terminate(job, SIGKILL)) return false;
    } else if (kill(-pid, SIGKILL) < 0 && errno != ESRCH) return false;
    pid_t result;
    do { result = waitpid(pid, NULL, 0); } while (result < 0 && errno == EINTR);
    return result == pid;
}

typedef int (*km_argv_launch)(const char *, void *);

static const char *km_argv_environment(char **envp, const char *name, bool windows) {
    size_t n = strlen(name);
    for (char **entry = envp; *entry; entry++) {
        size_t i = 0;
        for (; i < n && (*entry)[i]; i++) {
            unsigned char c = (unsigned char)(*entry)[i];
            if (windows && c >= 'a' && c <= 'z') c -= 'a' - 'A';
            if (c != (unsigned char)name[i]) break;
        }
        if (i == n && (*entry)[i] == '=') return *entry + i + 1;
    }
    return NULL;
}

static bool km_argv_slash(char c) { return c == '/' || c == '\\'; }

// Reserve only forward-slash /[A-Za-z] followed by '/' or end-of-string for
// Cosmo's drive alias. Native \\C\\... is rooted, not a cross-drive alias.
static bool km_argv_drive_alias(const char *path) {
    return path[0] == '/' && ((path[1] >= 'A' && path[1] <= 'Z') ||
                             (path[1] >= 'a' && path[1] <= 'z')) &&
           (path[2] == '/' || !path[2]);
}

// cwd is an absolute native Windows path, not a drive-relative path. Leave
// normalization of dot components to Cosmo; preserve UNC's leading slashes.
static char *km_argv_windows_path(const char *cwd, const char *name) {
    size_t n = strlen(name), base = 0;
    const char *prefix = cwd;
    bool alias = km_argv_drive_alias(name);
    bool cwd_alias = cwd && km_argv_drive_alias(cwd);
    char drive_prefix[3] = {cwd_alias ? cwd[1] : 0, ':', 0};
    bool drive = n >= 2 && name[1] == ':';
    if (drive && (n < 3 || !km_argv_slash(name[2]))) { errno = ENOTSUP; return NULL; }
    if (!drive && !alias && km_argv_slash(name[0])) {
        if (!km_argv_slash(name[1])) {
            if (cwd_alias) prefix = drive_prefix;
            else if (!cwd || strlen(cwd) < 3 || cwd[1] != ':' || !km_argv_slash(cwd[2])) {
                errno = ENOTSUP; return NULL;
            }
            base = 2; // Rooted paths use the stage's drive, never the parent's.
        }
    } else if (!drive && !alias) {
        if (!cwd || !*cwd) { errno = ENOTSUP; return NULL; }
        base = strlen(cwd);
    }
    bool slash = (base > 2 || (base == 2 && cwd_alias && prefix == cwd)) && !km_argv_slash(prefix[base - 1]);
    if (base > SIZE_MAX - n - 2) { errno = ENOMEM; return NULL; }
    char *path = malloc(base + n + 2);
    if (!path) { errno = ENOMEM; return NULL; }
    if (base) memcpy(path, prefix, base);
    if (slash) path[base++] = '/';
    memcpy(path + base, name, n + 1);
    if (alias || (cwd_alias && prefix == cwd && base)) {
        path[0] = path[1]; path[1] = ':';
        if (!path[2]) { path[2] = '/'; path[3] = 0; }
    }
    for (char *p = path; *p; p++) if (*p == '\\') *p = '/';
    return path;
}

// Cosmo getcwd returns /C/... on Windows, also accepted in resolved argv and
// PATH entries. Preserve original delimiters until rooted resolution, so
// neither \\C\\work nor /C\\work can turn into a forward drive alias by accident.
// The buffer needs one spare byte for the /C -> C:/ drive-root case.
static void km_argv_native_cwd(char *cwd) {
    if (km_argv_drive_alias(cwd)) {
        cwd[0] = cwd[1]; cwd[1] = ':';
        if (!cwd[2]) { cwd[2] = '/'; cwd[3] = 0; }
    }
}

static char *km_argv_windows_cwd(const char *cwd) {
    char current[PATH_MAX];
    if (!getcwd(current, sizeof(current))) return NULL;
    km_argv_native_cwd(current);
    if (!cwd) return km_argv_windows_path(current, "");
    char *native = malloc(strlen(cwd) + 2);
    if (!native) { errno = ENOMEM; return NULL; }
    strcpy(native, cwd);
    km_argv_native_cwd(native);
    char *result = km_argv_windows_path(current, native);
    int error = errno;
    free(native);
    errno = error;
    return result;
}

static bool km_argv_search_error(int error, int *saved) {
    if (error == EACCES) *saved = EACCES;
    return error == ENOENT || error == ENOTDIR || error == EACCES;
}

// Return launch errors directly. In particular, ordinary BAT/CMD text is
// ENOEXEC in pinned Cosmo ntspawn; never synthesize a cmd.exe command string.
// Explicit paths try the exact file first. Bare names keep PATHEXT order in
// each PATH directory, then try that directory's exact extensionless file.
static int km_argv_candidate(const char *path, const char *extensions, bool windows, bool explicit,
                             km_argv_launch launch, void *context, int *saved) {
    const char *base = path;
    for (const char *p = path; *p; p++) if (km_argv_slash(*p)) base = p + 1;
    bool exact_first = !windows || explicit || strchr(base, '.');
    if (exact_first) {
        int error = launch(path, context);
        if (!km_argv_search_error(error, saved)) return error;
    }
    if (windows) for (const char *part = extensions;;) {
        const char *end = strchr(part, ';');
        size_t n = end ? (size_t)(end - part) : strlen(part);
        if (n > 1 && part[0] == '.') {
            size_t len = strlen(path);
            if (len > SIZE_MAX - n - 1) return ENOMEM;
            char *option = malloc(len + n + 1);
            if (!option) return ENOMEM;
            memcpy(option, path, len); memcpy(option + len, part, n); option[len + n] = 0;
            int error = launch(option, context);
            free(option);
            if (!km_argv_search_error(error, saved)) return error;
        }
        if (!end) break;
        part = end + 1;
    }
    if (windows && !exact_first) {
        int error = launch(path, context);
        if (!km_argv_search_error(error, saved)) return error;
    }
    return -1; // All candidates exhausted; saved retains EACCES preference.
}

static int km_argv_lookup(const char *name, char **envp, const char *cwd, bool windows,
                          km_argv_launch launch, void *context) {
    if (windows && !*name) return ENOENT;
    const char *extensions = km_argv_environment(envp, "PATHEXT", windows);
    if (!extensions || !*extensions) extensions = ".COM;.EXE;.BAT;.CMD";
    int saved = ENOENT;
    if (strchr(name, '/') || (windows && (strchr(name, '\\') || strchr(name, ':')))) {
        if (!windows) return launch(name, context);
        char *path = km_argv_windows_path(cwd, name);
        if (!path) return errno;
        int error = km_argv_candidate(path, extensions, true, true, launch, context, &saved);
        free(path);
        return error == -1 ? saved : error;
    }
    const char *paths = km_argv_environment(envp, "PATH", windows);
    if (!paths) paths = windows ? "" : "/bin:/usr/bin";
    for (const char *part = paths;;) {
        const char *end = strchr(part, windows ? ';' : ':');
        size_t n = end ? (size_t)(end - part) : strlen(part), len = strlen(name);
        if (n > SIZE_MAX - len - 2) return ENOMEM;
        char *path = malloc(n + len + 2);
        if (!path) return ENOMEM;
        if (windows) {
            memcpy(path, part, n); path[n] = 0;
            char *directory = km_argv_windows_path(cwd, path);
            int error = errno;
            free(path); path = NULL;
            if (directory) {
                path = km_argv_windows_path(directory, name);
                error = errno;
                free(directory);
            }
            if (!path && error != ENOTSUP) return error;
        } else {
            if (n) { memcpy(path, part, n); path[n++] = '/'; }
            memcpy(path + n, name, len + 1);
        }
        if (path) {
            int error = km_argv_candidate(path, extensions, windows, false, launch, context, &saved);
            free(path);
            if (error != -1) return error;
        }
        if (!end) return saved;
        part = end + 1;
    }
}

typedef struct km_exec_argv_context { char **argv, **envp; } km_exec_argv_context;
static int km_exec_argv_launch(const char *path, void *opaque) {
    km_exec_argv_context *context = opaque;
    execve(path, context->argv, context->envp);
    return errno;
}

static void km_exec_argv(char **argv, char **envp) {
    bool windows = km_cli_environment_names_case_insensitive();
    char *cwd = windows ? km_argv_windows_cwd(NULL) : NULL;
    if (windows && !cwd) return;
    km_exec_argv_context context = {argv, envp};
    int error = km_argv_lookup(argv[0], envp, cwd, windows, km_exec_argv_launch, &context);
    free(cwd);
    errno = error;
}

// Resolve PATH using the request environment and launch through Cosmopolitan's
// file-action path, which maps pipe descriptors into native child handles.
#if defined(__COSMOPOLITAN__)
static bool km_spawn_trace_enabled(char **envp) {
    for (char **entry = envp; *entry; entry++) {
        if (strcmp(*entry, "KAME_WINDOWS_SPAWN_TRACE=1") == 0) return true;
    }
    return false;
}

typedef struct km_spawn_argv_context {
    pid_t *pid;
    char **argv, **envp;
    posix_spawn_file_actions_t *actions;
    posix_spawnattr_t *attr;
} km_spawn_argv_context;

static int km_spawn_argv_launch(const char *path, void *opaque) {
    km_spawn_argv_context *context = opaque;
    return posix_spawn(context->pid, path, context->actions, context->attr, context->argv, context->envp);
}

static int km_spawn_argv(pid_t *pid, char **argv, char **envp, const char *cwd, posix_spawn_file_actions_t *actions, posix_spawnattr_t *attr) {
    bool windows = km_cli_environment_names_case_insensitive();
    char *directory = windows ? km_argv_windows_cwd(cwd) : NULL;
    if (windows && !directory) return errno;
    km_spawn_argv_context context = {pid, argv, envp, actions, attr};
    int error = km_argv_lookup(argv[0], envp, directory, windows, km_spawn_argv_launch, &context);
    free(directory);
    return error;
}

static int km_spawn_graph_stage(pid_t *pid, char **argv, char **envp, char *cwd, int input, int output, int error_output, int out[2], int err[2], int execerr[2], int *edges, int edge_count, int inputfd, int outputfd) {
    bool trace = km_spawn_trace_enabled(envp);
    posix_spawn_file_actions_t actions;
    posix_spawnattr_t attr;
    int error = posix_spawn_file_actions_init(&actions);
    if (error) return error;
    error = posix_spawnattr_init(&attr);
    if (error) { posix_spawn_file_actions_destroy(&actions); return error; }
    if (trace) fprintf(stderr, "[windows-spawn] begin argv=%s stdin=%d stdout=%d stderr=%d cwd=%s inputfd=%d outputfd=%d\n", argv[0], input, output, error_output, cwd, inputfd, outputfd);
    if ((error = posix_spawn_file_actions_adddup2(&actions, input, STDIN_FILENO)) ||
        (error = posix_spawn_file_actions_adddup2(&actions, output, STDOUT_FILENO)) ||
        (error = posix_spawn_file_actions_adddup2(&actions, error_output, STDERR_FILENO)) ||
        (error = posix_spawn_file_actions_addchdir_np(&actions, cwd))) goto done;
    int close_fds[8] = {out[0], out[1], err[0], err[1], execerr[0], execerr[1], inputfd, outputfd};
    for (int i = 0; i < 8 + edge_count; i++) {
        int fd = i < 8 ? close_fds[i] : edges[i - 8];
        if (fd < 0 || fd <= STDERR_FILENO) continue;
        error = posix_spawn_file_actions_addclose(&actions, fd);
        if (error) goto done;
    }
    error = posix_spawnattr_setpgroup(&attr, 0);
    if (!error) error = posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETPGROUP);
    if (!error) error = km_spawn_argv(pid, argv, envp, cwd, &actions, &attr);
done:
    if (trace) fprintf(stderr, "[windows-spawn] done argv=%s error=%d pid=%d\n", argv[0], error, (int)*pid);
    posix_spawnattr_destroy(&attr);
    posix_spawn_file_actions_destroy(&actions);
    return error;
}
#endif

int km_host_start(km_host *host, int64_t id, so_Slice shell, so_Slice script, so_String directory, so_Slice environment, int64_t timeout, so_int retain, bool direct) {
    if (!host) return -1;
    // Bounded emergency rejection slot: -2 means not admitted; drain Next first.
    if (host->rejected_sequence) return -2;
    // Every rejected request emits exactly one failed terminal event.
    if (id == 0 || shell.len == 0 || timeout < 0 || retain < 0) return km_spawn_failed(host, id, "invalid request");
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return km_spawn_failed(host, id, "request ID already active");
    int out[2] = {-1, -1}, err[2] = {-1, -1}, execerr[2] = {-1, -1}, ready[2] = {-1, -1};
    int64_t job = km_windows_job_create();
    if (job < 0) goto fail;
    if (km_windows_jobs_needed() && pipe(ready)) goto fail;
    if (pipe(out) || pipe(err) || pipe(execerr)) goto fail;
    if (fcntl(execerr[1], F_SETFD, FD_CLOEXEC) < 0) goto fail;
    char **argv = calloc((size_t)shell.len + 2, sizeof(char *));
    char **envp = calloc((size_t)environment.len + 1, sizeof(char *));
    char *cwd = km_cstring(directory);
    if (!argv || !envp || !cwd || (script.len && (!script.ptr || memchr(script.ptr, '\0', (size_t)script.len)))) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    so_String *shells = shell.ptr, *envs = environment.ptr;
    for (int i = 0; i < shell.len; i++) {
        argv[i] = km_cstring(shells[i]);
        if (!argv[i]) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    }
    if (!direct) {
        argv[shell.len] = calloc((size_t)script.len + 1, 1);
        if (!argv[shell.len]) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
        if (script.len) memcpy(argv[shell.len], script.ptr, (size_t)script.len);
    }
    for (int i = 0; i < environment.len; i++) {
        envp[i] = km_cstring(envs[i]);
        if (!envp[i]) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    }
    pid_t pid = fork();
    if (pid < 0) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    if (pid == 0) {
        int child_errno;
        close(out[0]); close(err[0]); close(execerr[0]);
        if (ready[0] >= 0) {
            close(ready[1]);
            char token = 0;
            ssize_t n;
            do { n = read(ready[0], &token, 1); } while (n < 0 && errno == EINTR);
            close(ready[0]);
            if (n != 1 || token != 1) _exit(126);
        }
        if (setpgid(0, 0) || dup2(out[1], STDOUT_FILENO) < 0 || dup2(err[1], STDERR_FILENO) < 0 || chdir(cwd) < 0) {
            child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127);
        }
        close(out[1]); close(err[1]);
        if (direct) {
            int input = open("/dev/null", O_RDONLY);
            if (input < 0 || dup2(input, STDIN_FILENO) < 0) { child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127); }
            if (input != STDIN_FILENO) close(input);
            km_exec_argv(argv, envp);
        } else execve(argv[0], argv, envp);
        child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127);
    }
    km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd);
    close(out[1]); close(err[1]); close(execerr[1]); out[1] = err[1] = execerr[1] = -1;
    setpgid(pid, pid);
    if (ready[0] >= 0) {
        km_close(&ready[0]);
        if (!km_windows_job_assign(job, pid)) {
            km_close(&ready[1]);
            bool reaped = km_abort_spawn(pid, job);
            km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]); km_windows_job_close(&job);
            return km_spawn_failed(host, id, reaped ? "process group setup failed" : "waitpid failed");
        }
        char token = 1;
        ssize_t written;
        do { written = write(ready[1], &token, 1); } while (written < 0 && errno == EINTR);
        km_close(&ready[1]);
        if (written != 1) {
            bool reaped = km_abort_spawn(pid, job);
            km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]); km_windows_job_close(&job);
            return km_spawn_failed(host, id, reaped ? "process group setup failed" : "waitpid failed");
        }
    }
    if (km_set_nonblock(out[0]) || km_set_nonblock(err[0]) || km_set_nonblock(execerr[0])) { bool reaped = km_abort_spawn(pid, job); km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]); km_windows_job_close(&job); return km_spawn_failed(host, id, reaped ? "spawn failed" : "waitpid failed"); }
    if (host->len == host->cap) {
        int cap = host->cap ? host->cap * 2 : 4;
        km_process *processes = realloc(host->processes, (size_t)cap * sizeof(*processes));
        if (!processes) { bool reaped = km_abort_spawn(pid, job); km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]); km_windows_job_close(&job); return km_spawn_failed(host, id, reaped ? "spawn failed" : "waitpid failed"); }
        host->processes = processes; host->cap = cap;
    }
    km_process *p = &host->processes[host->len++];
    memset(p, 0, sizeof(*p)); p->id = id; p->pid = pid; p->job = job; p->outfd = out[0]; p->errfd = err[0]; p->execfd = execerr[0]; p->retain = retain; p->deadline = timeout ? km_now() + timeout : 0;
    p->direct = direct;
    return 0;
fail:
    km_close(&ready[0]); km_close(&ready[1]); km_windows_job_close(&job);
    km_close(&out[0]); km_close(&out[1]); km_close(&err[0]); km_close(&err[1]); km_close(&execerr[0]); km_close(&execerr[1]);
    return km_spawn_failed(host, id, "spawn failed");
}

// Flattened argv plus per-stage lengths keeps the ABI independent of generated
// Go structs. OS pipes carry intermediate bytes directly, with backpressure.
int km_host_start_graph(km_host *host, int64_t id, so_Slice arguments, so_Slice lengths, so_Slice directories, so_Slice environment, so_Slice environment_lengths, so_Slice timeouts, int64_t timeout, so_int retain, so_String input_path, so_String output_path, bool append_output) {
    so_Slice empty = {0};
    return km_host_start_topology(host, id, arguments, lengths, directories, environment, environment_lengths, timeouts, timeout, retain, input_path, output_path, append_output, empty, empty, empty, empty, empty, empty, empty, NULL, NULL, false);
}

bool km_stream_operands_supported(void) {
    if (km_windows_jobs_needed()) return false;
    int descriptors[2];
    if (pipe(descriptors)) return false;
    char name[64];
    snprintf(name, sizeof(name), "/dev/fd/%d", descriptors[0]);
    int reopened = open(name, O_RDONLY);
    struct stat original, exposed;
    bool supported = reopened >= 0 && !fstat(descriptors[0], &original) && !fstat(reopened, &exposed) &&
        original.st_dev == exposed.st_dev && original.st_ino == exposed.st_ino;
    if (reopened >= 0) close(reopened);
    close(descriptors[0]); close(descriptors[1]);
    return supported;
}

// One long-running native operator node, owned and reaped like a process stage.
// It holds one bounded chunk, never captures a producer or spawns per record.
static bool km_mux_keeps_fd(int fd, int node, int *edges, so_int *links, int count) {
    for (int i = 0; i < count; i++) if ((links[i * 2] == node && edges[i * 2 + 1] == fd) || (links[i * 2 + 1] == node && edges[i * 2] == fd)) return true;
    return false;
}

static void km_mux_close_unowned(int node, int *edges, so_int *links, int count) {
    // Unlike exec, a native worker does not apply FD_CLOEXEC automatically.
    // Do not retain another async graph's readers, locks, or host descriptors.
    DIR *directory = opendir("/proc/self/fd");
    if (!directory) directory = opendir("/dev/fd");
    if (directory) {
        int own = dirfd(directory);
        struct dirent *entry;
        while ((entry = readdir(directory))) {
            int fd = atoi(entry->d_name);
            if (fd > 2 && fd != own && !km_mux_keeps_fd(fd, node, edges, links, count)) close(fd);
        }
        closedir(directory);
    } else {
        long limit = sysconf(_SC_OPEN_MAX);
        if (limit < 0) _exit(125);
        for (int fd = 3; fd < limit; fd++) if (!km_mux_keeps_fd(fd, node, edges, links, count)) close(fd);
    }
}

enum { KM_RECORD_LIMIT = 64 * 1024, KM_FRAME_ENCODING = 120, KM_RECORD_TOO_LONG = 121, KM_PREDICATE_FAILED = 124, KM_BUFFER_LIMIT = 126 };

// These globals belong to one forked operator, never to the host or another
// invocation. Each edge has one measurement owner. Relaxed atomic fields let
// the parent sample progress without stopping workers. A live row is not a
// transactional cross-field snapshot; final rows are sampled after child exit.
static int64_t *km_worker_metrics;
static int *km_worker_state;
static int *km_worker_pipes;
static so_int *km_worker_links;
static int km_worker_node, km_worker_edges;

static void km_worker_wait(int state) {
    if (km_worker_state) __atomic_store_n(km_worker_state, state, __ATOMIC_RELAXED);
}

static void km_metric_add(int64_t *metric, int field, int64_t count) {
    const int64_t limit = INT64_C(9007199254740991); // exact on both native and JS
    int64_t value = __atomic_load_n(metric + field, __ATOMIC_RELAXED);
    if (count > limit - value) { value = limit; __atomic_store_n(metric + KM_METRIC_SATURATED, 1, __ATOMIC_RELAXED); }
    else value += count;
    __atomic_store_n(metric + field, value, __ATOMIC_RELAXED);
}

static void km_metric_read(int fd, int length) {
    if (!km_worker_metrics || length <= 0) return;
    for (int i = 0; i < km_worker_edges; i++) {
        int64_t *metric = km_worker_metrics + i * KM_METRIC_FIELDS;
        if (metric[KM_METRIC_MODE] == 2 && km_worker_links[i * 2 + 1] == km_worker_node && km_worker_pipes[i * 2] == fd) {
            km_metric_add(metric, KM_METRIC_BYTES, length); return;
        }
    }
}

static void km_metric_write(int edge, int payload) {
    if (!km_worker_metrics) return;
    int64_t *metric = km_worker_metrics + edge * KM_METRIC_FIELDS;
    if (metric[KM_METRIC_MODE] != 1 && metric[KM_METRIC_MODE] != 3) return;
    km_metric_add(metric, KM_METRIC_BYTES, payload);
    if (metric[KM_METRIC_ITEMS] >= 0) km_metric_add(metric, KM_METRIC_ITEMS, 1);
}

static bool km_mux_write(int fd, const unsigned char *data, size_t length) {
    km_worker_wait(KM_WAIT_OUTPUT);
    while (length) {
        ssize_t written = write(fd, data, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return false;
        data += written; length -= (size_t)written;
    }
    km_worker_wait(KM_RUNNING); return true;
}

// The private envelope is uint32 little-endian length followed by exact raw
// bytes. Only explicit framers create envelopes; executables never see them.
static int km_mux_send(int node, int kind, int *edges, so_int *links, int count,
                       unsigned char *data, int length, int selected) {
    int output = 0;
    for (int i = 0; i < count; i++) if (links[i * 2] == node) {
        bool deliver = (kind != 5 && kind != 7) || output == selected;
        output++;
        if (deliver) {
            if (!km_mux_write(edges[i * 2 + 1], data, (size_t)length)) return 125;
            int header = km_worker_metrics && km_worker_metrics[i * KM_METRIC_FIELDS + KM_METRIC_ITEMS] >= 0 ? 4 : 0;
            km_metric_write(i, length - header);
        }
    }
    if (!output && !km_mux_write(STDOUT_FILENO, data, (size_t)length)) return 125;
    return 0;
}

static int km_mux_frame(int node, int *edges, so_int *links, int count,
                        int64_t id, km_stream_item item, void *context) {
    int input = -1;
    for (int i = 0; i < count; i++) if (links[i * 2 + 1] == node) input = edges[i * 2];
    unsigned char *record = malloc(KM_RECORD_LIMIT + 4);
    if (!record || input < 0) { free(record); return 125; }
    unsigned char chunk[KM_CHUNK];
    int length = 0, result = 0;
    for (;;) {
        ssize_t n;
        km_worker_wait(KM_WAIT_INPUT);
        do { n = read(input, chunk, sizeof(chunk)); } while (n < 0 && errno == EINTR);
        km_worker_wait(KM_RUNNING);
        if (n < 0) { result = 125; break; }
        km_metric_read(input, (int)n);
        for (int i = 0; i < n || (!n && length); i++) {
            if (n) {
                if (length == KM_RECORD_LIMIT) { result = KM_RECORD_TOO_LONG; break; }
                record[4 + length++] = chunk[i];
                if (chunk[i] != '\n') continue;
            }
            so_Slice raw = {.ptr = record + 4, .len = length, .cap = length};
            int valid = item ? (int)item(context, id, -1, raw) : -KM_PREDICATE_FAILED;
            if (valid < 0) { result = -valid; break; }
            for (int j = 0; j < 4; j++) record[j] = (unsigned char)((unsigned)length >> (j * 8));
            result = km_mux_send(node, 3, edges, links, count, record, length + 4, 0);
            length = 0;
            if (result || !n) break;
        }
        if (result || !n) break;
    }
    free(record);
    return result;
}

typedef struct km_frame_input { unsigned char *data; int length, need; } km_frame_input;

typedef struct km_queued_record { unsigned char *data; int length, offset; } km_queued_record;
typedef struct km_record_queue {
    km_queued_record *pending, flight;
    int head, count, bytes;
    int edge;
} km_record_queue;

static km_queued_record km_queue_pop(km_record_queue *queue, int limit) {
    km_queued_record record = queue->pending[queue->head];
    queue->pending[queue->head] = (km_queued_record){0};
    queue->head = (queue->head + 1) % limit;
    queue->count--; queue->bytes -= record.length - 4;
    return record;
}

static bool km_queue_push(km_record_queue *queue, int limit, int budget, int policy,
                          unsigned char *data, int length) {
    int payload = length - 4;
    int64_t *metric = km_worker_metrics ? km_worker_metrics + queue->edge * KM_METRIC_FIELDS : NULL;
    if (policy == 1 && (queue->count == limit || queue->bytes + payload > budget)) { if (metric) km_metric_add(metric, KM_METRIC_DROPS, 1); return true; }
    while (queue->count && (policy == 3 || queue->count == limit || queue->bytes + payload > budget)) {
        km_queued_record old = km_queue_pop(queue, limit);
        free(old.data);
        if (metric) km_metric_add(metric, KM_METRIC_DROPS, 1);
    }
    unsigned char *copy = malloc((size_t)length);
    if (!copy) return false;
    memcpy(copy, data, (size_t)length);
    queue->pending[(queue->head + queue->count) % limit] = (km_queued_record){.data = copy, .length = length};
    queue->count++; queue->bytes += payload;
    if (metric) {
        if (queue->bytes > __atomic_load_n(metric + KM_METRIC_HIGH_BYTES, __ATOMIC_RELAXED)) __atomic_store_n(metric + KM_METRIC_HIGH_BYTES, queue->bytes, __ATOMIC_RELAXED);
        if (queue->count > __atomic_load_n(metric + KM_METRIC_HIGH_ITEMS, __ATOMIC_RELAXED)) __atomic_store_n(metric + KM_METRIC_HIGH_ITEMS, queue->count, __ATOMIC_RELAXED);
    }
    return true;
}

// Each destination has a bounded pending ring plus one protected in-flight
// record. Never evict a partially written envelope: that would corrupt framing.
static int km_run_lossy_broadcast(int node, int *edges, so_int *links, int count,
                                  int budget, int limit, int policy) {
    int outputs = 0, input = -1;
    for (int i = 0; i < count; i++) {
        if (links[i * 2] == node) outputs++;
        if (links[i * 2 + 1] == node) input = edges[i * 2];
    }
    if (input < 0 || outputs < 2 || policy < 1 || policy > 3 || limit < 0 || limit > 1024) return 125;
    if (!limit) limit = policy == 3 ? 1 : 64;
    if (policy == 3 && limit != 1) return 125;
    struct pollfd *fds = calloc((size_t)outputs + 1, sizeof(*fds));
    km_record_queue *queues = calloc((size_t)outputs, sizeof(*queues));
    unsigned char *record = malloc((size_t)budget + 4);
    int result = 0;
    if (!fds || !queues || !record) { result = 125; goto cleanup; }
    fds[0] = (struct pollfd){.fd = input, .events = POLLIN};
    int at = 1;
    for (int i = 0; i < count; i++) if (links[i * 2] == node) {
        int fd = edges[i * 2 + 1], flags = fcntl(fd, F_GETFL);
        queues[at - 1].edge = i;
        fds[at++].fd = fd;
        if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK)) { result = 125; goto cleanup; }
    }
    for (int i = 0; i < outputs; i++) {
        queues[i].pending = calloc((size_t)limit, sizeof(km_queued_record));
        if (!queues[i].pending) { result = 125; goto cleanup; }
    }
    int active = outputs, length = 0, need = 4;
    while (active && !result) {
        bool pending = false;
        for (int i = 0; i < outputs; i++) {
            km_record_queue *queue = &queues[i];
            if (fds[i + 1].fd < 0) continue;
            if (fds[0].fd < 0 && !queue->count && !queue->flight.data) {
                close(fds[i + 1].fd); fds[i + 1].fd = -1; active--;
            }
            fds[i + 1].events = queue->count || queue->flight.data ? POLLOUT : 0;
            if (fds[i + 1].events) pending = true;
        }
        if (!active) break;
        int ready;
        km_worker_wait(pending ? KM_WAIT_OUTPUT : KM_WAIT_INPUT);
        do { ready = poll(fds, (nfds_t)outputs + 1, -1); } while (ready < 0 && errno == EINTR);
        km_worker_wait(KM_RUNNING);
        if (ready < 0) { result = 125; break; }
        for (int i = 0; i < outputs; i++) {
            if (fds[i + 1].revents & (POLLERR | POLLHUP | POLLNVAL)) { result = 125; break; }
            if (!(fds[i + 1].revents & POLLOUT)) continue;
            km_record_queue *queue = &queues[i];
            if (!queue->flight.data && queue->count) queue->flight = km_queue_pop(queue, limit);
            km_queued_record *flight = &queue->flight;
            if (!flight->data) continue;
            ssize_t n;
            do { n = write(fds[i + 1].fd, flight->data + flight->offset, (size_t)(flight->length - flight->offset)); } while (n < 0 && errno == EINTR);
            if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) continue;
            if (n <= 0) { result = 125; break; }
            flight->offset += (int)n;
            if (flight->offset == flight->length) { km_metric_write(queue->edge, flight->length - 4); free(flight->data); *flight = (km_queued_record){0}; }
        }
        if (result || fds[0].fd < 0 || !fds[0].revents) continue;
        ssize_t n;
        do { n = read(fds[0].fd, record + length, (size_t)(need - length)); } while (n < 0 && errno == EINTR);
        if (n < 0) { result = 125; break; }
        if (!n) {
            if (length) { result = 125; break; }
            close(fds[0].fd); fds[0].fd = -1; continue;
        }
        length += (int)n;
        if (length != need) continue;
        if (need == 4) {
            uint32_t size = 0;
            for (int j = 0; j < 4; j++) size |= (uint32_t)record[j] << (j * 8);
            if (!size || size > KM_RECORD_LIMIT) { result = KM_RECORD_TOO_LONG; break; }
            if (size > (uint32_t)budget) { result = KM_BUFFER_LIMIT; break; }
            need = 4 + (int)size;
        } else {
            for (int i = 0; i < outputs; i++) if (!km_queue_push(&queues[i], limit, budget, policy, record, length)) { result = 125; break; }
            length = 0; need = 4;
        }
    }
cleanup:
    if (queues) for (int i = 0; i < outputs; i++) {
        while (queues[i].count) { km_queued_record old = km_queue_pop(&queues[i], limit); free(old.data); }
        free(queues[i].flight.data); free(queues[i].pending);
    }
    free(record); free(queues); free(fds);
    return result;
}

// Explicit per-record workers reuse one supervisor/process group. Only one
// payload and one child are active; stdin and stdout progress concurrently so
// a child may write more than a pipe's capacity before it consumes its input.
static int km_each_child(int node, int *edges, so_int *links, int count,
                          char **argv, char **envp, unsigned char *record, int size,
                          int *failure) {
    int input[2] = {-1, -1}, output[2] = {-1, -1}, launch[2] = {-1, -1};
    pid_t child = -1;
    int result = 0, status = 0;
    if (pipe(input) || pipe(output) || pipe(launch)) goto failed;
    for (int i = 0; i < 2; i++) if (fcntl(launch[i], F_SETFD, FD_CLOEXEC) < 0) goto failed;
    child = fork();
    if (child < 0) goto failed;
    if (!child) {
        close(launch[0]);
        if (dup2(input[0], STDIN_FILENO) < 0 || dup2(output[1], STDOUT_FILENO) < 0) {
            int error = errno; write(launch[1], &error, sizeof(error)); _exit(127);
        }
        close(input[0]); close(input[1]); close(output[0]); close(output[1]);
        signal(SIGPIPE, SIG_DFL);
        km_exec_argv(argv, envp);
        int error = errno; write(launch[1], &error, sizeof(error)); _exit(127);
    }
    km_close(&input[0]); km_close(&output[1]); km_close(&launch[1]);
    if (km_set_nonblock(input[1]) || km_set_nonblock(output[0])) goto failed;
    int offset = 0;
    unsigned char chunk[KM_CHUNK];
    while (input[1] >= 0 || output[0] >= 0) {
        struct pollfd fds[2] = {{.fd = input[1], .events = POLLOUT}, {.fd = output[0], .events = POLLIN}};
        int ready;
        km_worker_wait(KM_RUNNING); // arbitrary executable internal waits are unknown
        do { ready = poll(fds, 2, -1); } while (ready < 0 && errno == EINTR);
        if (ready < 0) goto failed;
        if (input[1] >= 0 && fds[0].revents) {
            ssize_t n = write(input[1], record + offset, (size_t)(size - offset));
            if (n < 0 && errno != EINTR && errno != EAGAIN) goto failed;
            if (n > 0) offset += (int)n;
            if (offset == size) km_close(&input[1]);
        }
        if (output[0] >= 0 && fds[1].revents) {
            ssize_t n = read(output[0], chunk, sizeof(chunk));
            if (n < 0 && errno != EINTR && errno != EAGAIN) goto failed;
            if (!n) km_close(&output[0]);
            if (n > 0 && km_mux_send(node, 8, edges, links, count, chunk, (int)n, 0)) goto failed;
        }
    }
    pid_t waited;
    do { waited = waitpid(child, &status, 0); } while (waited < 0 && errno == EINTR);
    if (waited != child) goto failed;
    child = -1;
    int error;
    ssize_t n;
    do { n = read(launch[0], &error, sizeof(error)); } while (n < 0 && errno == EINTR);
    if (n != 0) goto failed;
    if (WIFSIGNALED(status)) {
        int sig = WTERMSIG(status);
        signal(sig, SIG_DFL); raise(sig); _exit(128 + sig);
    }
    result = WEXITSTATUS(status);
    goto cleanup;
failed:
    __atomic_store_n(failure, 1, __ATOMIC_RELAXED);
    result = 125;
    if (child > 0) { kill(child, SIGKILL); while (waitpid(child, &status, 0) < 0 && errno == EINTR) {} }
cleanup:
    for (int i = 0; i < 2; i++) { km_close(&input[i]); km_close(&output[i]); km_close(&launch[i]); }
    return result;
}

static int km_run_each(int node, int *edges, so_int *links, int count,
                        char **argv, char **envp, int64_t *metrics, int *states,
                        int *failure, bool collect) {
    km_worker_state = states + node;
    km_worker_metrics = metrics; km_worker_pipes = edges; km_worker_links = links;
    km_worker_node = node; km_worker_edges = count;
    km_mux_close_unowned(node, edges, links, count);
    signal(SIGPIPE, SIG_IGN); signal(SIGTERM, SIG_DFL); signal(SIGINT, SIG_DFL);
    int input = -1, result = 0;
    for (int i = 0; i < count; i++) if (links[i * 2 + 1] == node) input = edges[i * 2];
    unsigned char *record = malloc(KM_RECORD_LIMIT + 4);
    if (!record || input < 0) { __atomic_store_n(failure, 1, __ATOMIC_RELAXED); free(record); return 125; }
    for (;;) {
        int length = 0, need = 4;
        while (length < need) {
            km_worker_wait(KM_WAIT_INPUT);
            ssize_t n = read(input, record + length, (size_t)(need - length));
            if (n < 0 && errno == EINTR) continue;
            if (!n && !length) goto complete;
            if (n <= 0) goto failed;
            length += (int)n;
            if (length == 4 && need == 4) {
                uint32_t size = 0;
                for (int i = 0; i < 4; i++) size |= (uint32_t)record[i] << (8 * i);
                if (!size || size > KM_RECORD_LIMIT) goto failed;
                need += (int)size;
            }
        }
        km_worker_wait(KM_RUNNING);
        int status = km_each_child(node, edges, links, count, argv, envp, record + 4, length - 4, failure);
        if (status) result = status;
        if (__atomic_load_n(failure, __ATOMIC_RELAXED) || (status && !collect)) break;
    }
    goto complete;
failed:
    __atomic_store_n(failure, 1, __ATOMIC_RELAXED); result = 125;
complete:
    free(record); return result;
}

static int km_run_mux(int node, int mode, int *edges, so_int *links, int count,
                      int item_limit, int64_t *metrics, int *states, int64_t id, km_stream_item item, void *context) {
    km_worker_state = states ? states + node : NULL;
    km_worker_metrics = metrics; km_worker_pipes = edges; km_worker_links = links;
    km_worker_node = node; km_worker_edges = count;
    int kind = mode & 15;
    bool framed = (mode & 16) || kind == 4;
    bool ordered = (mode & 128) != 0;
    if (ordered && (kind != 1 || !framed)) return 125;
    int budget = mode >> 8;
    if (!budget) budget = framed ? KM_RECORD_LIMIT : KM_CHUNK;
    if (budget < 1 || budget > KM_RECORD_LIMIT) return 125;
    struct pollfd *inputs = calloc((size_t)count, sizeof(*inputs));
    if (!inputs) return 125;
    int readers = 0;
    for (int i = 0; i < count; i++) {
        if (links[i * 2 + 1] == node) inputs[readers++] = (struct pollfd){.fd = edges[i * 2], .events = POLLIN};
    }
    km_mux_close_unowned(node, edges, links, count);
    signal(SIGPIPE, SIG_DFL); signal(SIGTERM, SIG_DFL); signal(SIGINT, SIG_DFL);
    int overflow = (mode >> 5) & 3;
    if (overflow) {
        free(inputs);
        if (kind != 2 || !framed || readers != 1) return 125;
        return km_run_lossy_broadcast(node, edges, links, count, budget, item_limit, overflow);
    }
    if (kind == 3) { free(inputs); return km_mux_frame(node, edges, links, count, id, item, context); }
    km_frame_input *frames = framed ? calloc((size_t)readers, sizeof(*frames)) : NULL;
    if (framed && !frames) { free(inputs); return 125; }
    int result = 0;
    for (int i = 0; framed && i < readers; i++) {
        frames[i].data = malloc((size_t)budget + 4); frames[i].need = 4;
        if (!frames[i].data) { result = 125; break; }
    }
    int active = readers, cursor = 0, destination = 0, outputs = 0;
    for (int i = 0; i < count; i++) if (links[i * 2] == node) outputs++;
    if (kind == 7 && (!framed || readers != 1 || outputs < 2)) result = 125;
    unsigned char *buffer = framed ? NULL : malloc((size_t)budget);
    if (!framed && !buffer) result = 125;
    while (active && !result) {
        int ready;
        km_worker_wait(KM_WAIT_INPUT);
        if (ordered) while (inputs[cursor].fd < 0) cursor = (cursor + 1) % readers;
        do { ready = ordered ? poll(inputs + cursor, 1, -1) : poll(inputs, (nfds_t)readers, -1); } while (ready < 0 && errno == EINTR);
        km_worker_wait(KM_RUNNING);
        if (ready < 0) { result = 125; break; }
        for (int offset = 0; offset < readers; offset++) {
            int next = (cursor + offset) % readers;
            if (ordered && offset != 0) break;
            if (inputs[next].fd < 0 || !inputs[next].revents) continue;
            ssize_t n;
            km_frame_input *frame = framed ? &frames[next] : NULL;
            unsigned char *data = framed ? frame->data + frame->length : buffer;
            size_t capacity = framed ? (size_t)(frame->need - frame->length) : (size_t)budget;
            do { n = read(inputs[next].fd, data, capacity); } while (n < 0 && errno == EINTR);
            if (n < 0) { result = 125; break; }
            km_metric_read(inputs[next].fd, (int)n);
            if (!n) {
                if (framed && frame->length) { result = 125; break; }
                close(inputs[next].fd); inputs[next].fd = -1; active--;
                if (ordered) { cursor = (next + 1) % readers; break; }
                continue;
            }
            if (!ordered) cursor = (next + 1) % readers;
            int length = (int)n, selected = 0;
            if (framed) {
                frame->length += (int)n;
                if (frame->length < frame->need) break;
                if (frame->need == 4) {
                    uint32_t size = 0;
                    for (int j = 0; j < 4; j++) size |= (uint32_t)frame->data[j] << (j * 8);
                    if (!size || size > KM_RECORD_LIMIT) { result = KM_RECORD_TOO_LONG; break; }
                    if (size > (uint32_t)budget) { result = KM_BUFFER_LIMIT; break; }
                    frame->need = 4 + (int)size;
                    break;
                }
                data = frame->data; length = frame->length;
                if (kind == 5) {
                    so_Slice raw = {.ptr = data + 4, .len = length - 4, .cap = length - 4};
                    int accepted = item ? (int)item(context, id, node, raw) : -KM_PREDICATE_FAILED;
                    if (accepted < 0) { result = -accepted; break; }
                    selected = accepted ? 0 : 1;
                }
                if (kind == 4) { data += 4; length -= 4; }
                if (kind == 7) { selected = destination; destination = (destination + 1) % outputs; }
            }
            result = km_mux_send(node, kind, edges, links, count, data, length, selected);
            if (framed) { frame->length = 0; frame->need = 4; }
            cursor = (next + 1) % readers;
            break;
        }
    }
    if (frames) { for (int i = 0; i < readers; i++) free(frames[i].data); free(frames); }
    free(buffer);
    free(inputs);
    return result;
}

int km_host_start_topology(km_host *host, int64_t id, so_Slice arguments, so_Slice lengths, so_Slice directories, so_Slice environment, so_Slice environment_lengths, so_Slice timeouts, int64_t timeout, so_int retain, so_String input_path, so_String output_path, bool append_output, so_Slice kinds, so_Slice connections, so_Slice input_paths, so_Slice output_paths, so_Slice appends, so_Slice operand_arguments, so_Slice item_limits, km_stream_item stream_item, void *stream_context, bool collect_failures) {
    if (!host) return -1;
    if (host->rejected_sequence) return -2;
    if (!id || lengths.len < 1 || lengths.len > INT_MAX / 2 || timeout < 0 || retain < 0) return km_spawn_failed(host, id, "invalid graph request");
    bool topology = kinds.len != 0;
    if (collect_failures && !topology) return km_spawn_failed(host, id, "failure collection requires a topology graph");
    if (topology && km_windows_jobs_needed()) return km_spawn_failed(host, id, "streaming process graphs are not supported by this Windows host");
    if (topology && (kinds.len != lengths.len || input_paths.len != lengths.len || output_paths.len != lengths.len || appends.len != lengths.len || connections.len % 2 || connections.len > INT_MAX / 2)) return km_spawn_failed(host, id, "invalid graph topology");
    so_int *node_kinds = kinds.ptr, *links = connections.ptr;
    int edge_count = topology ? (int)connections.len / 2 : (int)lengths.len - 1;
    if (topology && (operand_arguments.len != edge_count || item_limits.len != lengths.len)) return km_spawn_failed(host, id, "invalid graph ports or limits");
    so_int *ports = operand_arguments.ptr;
    for (int i = 0; topology && i < connections.len; i++) if (links[i] < 0 || links[i] >= lengths.len) return km_spawn_failed(host, id, "invalid graph edge");
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return km_spawn_failed(host, id, "request ID already active");
    so_int *counts = lengths.ptr;
    so_int total = 0;
    for (int i = 0; i < lengths.len; i++) {
        if (counts[i] < 0 || (!counts[i] && (!topology || !node_kinds[i])) || counts[i] > arguments.len - total) return km_spawn_failed(host, id, "invalid graph argv");
        total += counts[i];
    }
    if (total != arguments.len) return km_spawn_failed(host, id, "invalid graph argv");
    int n = (int)lengths.len;
    bool live_operands = false;
    for (int i = 0; topology && i < edge_count; i++) {
        int target = (int)links[i * 2 + 1];
        if (ports[i] < 0 || (ports[i] && (node_kinds[target] || ports[i] >= counts[target]))) return km_spawn_failed(host, id, "invalid live stream operand");
        if (ports[i]) live_operands = true;
        for (int j = 0; ports[i] && j < i; j++) if (links[j * 2 + 1] == target && ports[j] == ports[i]) return km_spawn_failed(host, id, "duplicate live stream operand");
    }
    if (live_operands && !km_stream_operands_supported()) return km_spawn_failed(host, id, "live stream operands are unsupported by this host");
    if (directories.len != n || environment_lengths.len != n || timeouts.len != n) return km_spawn_failed(host, id, "invalid graph setup");
    so_int *envcounts = environment_lengths.ptr;
    int64_t *stage_timeouts = timeouts.ptr;
    so_int envtotal = 0;
    for (int i = 0; i < n; i++) {
        if (envcounts[i] < 0 || envcounts[i] > environment.len - envtotal || stage_timeouts[i] < 0) return km_spawn_failed(host, id, "invalid graph setup");
        envtotal += envcounts[i];
    }
    if (envtotal != environment.len) return km_spawn_failed(host, id, "invalid graph environment");
    int out[2] = {-1,-1}, err[2] = {-1,-1}, execerr[2] = {-1,-1};
    int *edges = malloc((size_t)(edge_count ? edge_count : 1) * 2 * sizeof(int));
    int *operand_fds = malloc((size_t)(edge_count ? edge_count : 1) * sizeof(int));
    if (operand_fds) for (int i = 0; i < edge_count; i++) operand_fds[i] = -1;
    int *stage_inputs = malloc((size_t)n * sizeof(int)), *stage_outputs = malloc((size_t)n * sizeof(int));
    bool *truncate_outputs = calloc((size_t)n, sizeof(bool));
    if (stage_inputs) for (int i = 0; i < n; i++) stage_inputs[i] = -1;
    if (stage_outputs) for (int i = 0; i < n; i++) stage_outputs[i] = -1;
    int inputfd = -1, outputfd = -1;
    char *input_name = km_cstring(input_path), *output_name = km_cstring(output_path);
    char ***argv = calloc((size_t)n, sizeof(char **));
    char ***envp = calloc((size_t)n, sizeof(char **));
    char **cwd = calloc((size_t)n, sizeof(char *));
    km_process p = {.id = id, .outfd = -1, .errfd = -1, .execfd = -1, .direct = true, .retain = (int)retain, .stage_count = n, .topology = topology, .failed_stage = -1};
    p.collect_failures = collect_failures;
    p.stages = calloc((size_t)n, sizeof(km_stage));
    if (topology) {
        // First half: advisory wait states. Second half: authoritative worker
        // transport/launch errors, distinct from arbitrary executable statuses.
        void *mapping = mmap(NULL, (size_t)n * 2 * sizeof(int), PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
        if (mapping != MAP_FAILED) { p.live_states = mapping; for (int i = 0; i < n; i++) p.live_states[i] = KM_RUNNING; }
    }
    if (topology && edge_count && (size_t)edge_count <= SIZE_MAX / (KM_METRIC_FIELDS * sizeof(int64_t))) {
        size_t size = (size_t)edge_count * KM_METRIC_FIELDS * sizeof(int64_t);
        void *mapping = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
        if (mapping != MAP_FAILED) {
            p.metrics = mapping; p.metric_count = edge_count;
            for (int i = 0; i < edge_count; i++) {
                int from = (int)links[i * 2], to = (int)links[i * 2 + 1];
                int64_t *metric = p.metrics + i * KM_METRIC_FIELDS;
                metric[KM_METRIC_MODE] = node_kinds[from] ? ((node_kinds[from] >> 5) & 3) ? 3 : 1 : node_kinds[to] ? 2 : 0;
                metric[KM_METRIC_ITEMS] = metric[KM_METRIC_DROPS] = node_kinds[from] & 16 ? 0 : -1;
            }
        }
    }
    if (p.stages && topology) for (int i = 0; i < n; i++) p.stages[i].operator_kind = (int)node_kinds[i] & 15;
    if (edges) for (int i = 0; i < edge_count * 2; i++) edges[i] = -1;
    bool ok = edges && operand_fds && stage_inputs && stage_outputs && truncate_outputs && argv && envp && cwd && p.stages && input_name && output_name;
    for (int i = 0; topology && i < n; i++) if ((node_kinds[i] & 15) == 8 && !p.live_states) ok = false;
    so_String *args = arguments.ptr, *envs = environment.ptr, *dirs = directories.ptr;
    int at = 0;
    for (int i = 0; ok && i < n; i++) {
        argv[i] = calloc((size_t)counts[i] + 1, sizeof(char *));
        if (!argv[i]) { ok = false; break; }
        for (int j = 0; j < counts[i]; j++) {
            argv[i][j] = km_cstring(args[at++]);
            if (!argv[i][j] || (j == 0 && !argv[i][j][0])) { ok = false; break; }
        }
    }
    int envat = 0;
    for (int i = 0; ok && i < n; i++) {
        cwd[i] = km_cstring(dirs[i]);
        envp[i] = calloc((size_t)envcounts[i] + 1, sizeof(char *));
        if (!cwd[i] || !envp[i]) { ok = false; break; }
        struct stat directory_stat;
        if (stat(cwd[i], &directory_stat) || !S_ISDIR(directory_stat.st_mode)) { ok = false; break; }
        for (int j = 0; j < envcounts[i]; j++) { envp[i][j] = km_cstring(envs[envat++]); if (!envp[i][j]) { ok = false; break; } }
    }
    if (ok && (pipe(out) || pipe(err) || pipe(execerr))) ok = false;
    for (int i = 0; ok && i < edge_count; i++) {
        int pipe_status = km_windows_jobs_needed() ? km_windows_sync_pipe(edges + i * 2) : pipe(edges + i * 2);
        if (pipe_status) ok = false;
    }
    // Exec closes every unused descriptor, including the shared launch channel.
    for (int i = 0; ok && i < edge_count * 2; i++) if (fcntl(edges[i], F_SETFD, FD_CLOEXEC) < 0) ok = false;
    // Allocate collision-free inherited descriptors before opening/truncating
    // outputs. Other children retain CLOEXEC; only the receiving child clears it.
    for (int i = 0; ok && topology && i < edge_count; i++) if (ports[i]) {
        operand_fds[i] = fcntl(edges[i * 2], F_DUPFD, 3);
        char name[64];
        int size = snprintf(name, sizeof(name), "/dev/fd/%d", operand_fds[i]);
        if (operand_fds[i] < 0 || size < 0 || size >= (int)sizeof(name) || fcntl(operand_fds[i], F_SETFD, FD_CLOEXEC) < 0) { ok = false; break; }
        char *path = strdup(name);
        if (!path) { ok = false; break; }
        int target = (int)links[i * 2 + 1], argument = (int)ports[i];
        free(argv[target][argument]); argv[target][argument] = path;
    }
    if (ok && (fcntl(execerr[1], F_SETFD, FD_CLOEXEC) < 0 || km_set_nonblock(out[0]) || km_set_nonblock(err[0]) || km_set_nonblock(execerr[0]))) ok = false;
    if (ok && host->len == host->cap) {
        int cap = host->cap ? host->cap * 2 : 4;
        km_process *grown = realloc(host->processes, (size_t)cap * sizeof(km_process));
        if (!grown) ok = false;
        else { host->processes = grown; host->cap = cap; }
    }
    // Every argv is already validated. Open input before output so a missing
    // source cannot truncate the destination. Evaluator grants precede this call.
    if (ok && input_name[0]) { inputfd = open(input_name, O_RDONLY); if (inputfd < 0) ok = false; }
    for (int i = 0; ok && topology && i < n; i++) {
        char *name = km_cstring(((so_String *)input_paths.ptr)[i]);
        if (!name) ok = false;
        else if (name[0]) { stage_inputs[i] = open(name, O_RDONLY); if (stage_inputs[i] < 0) ok = false; }
        free(name);
    }
    if (ok && output_name[0]) { outputfd = open(output_name, O_WRONLY | O_CREAT | (append_output ? O_APPEND : O_TRUNC), 0666); if (outputfd < 0) ok = false; }
    for (int i = 0; ok && topology && i < n; i++) {
        char *name = km_cstring(((so_String *)output_paths.ptr)[i]);
        if (!name) ok = false;
        else if (name[0]) {
            bool append = ((so_int *)appends.ptr)[i] != 0;
            stage_outputs[i] = open(name, O_WRONLY | O_CREAT | (append ? O_APPEND : 0), 0666);
            struct stat info;
            if (stage_outputs[i] < 0 || fstat(stage_outputs[i], &info)) ok = false;
            else truncate_outputs[i] = !append && S_ISREG(info.st_mode);
        }
        free(name);
    }
    // Prepare every sink without destroying existing bytes. A later invalid
    // redirection must not truncate an earlier destination in another branch.
    for (int i = 0; ok && topology && i < n; i++) if (truncate_outputs[i] && ftruncate(stage_outputs[i], 0)) ok = false;
    if (ok && km_windows_jobs_needed() && inputfd < 0) { inputfd = open("/dev/null", O_RDONLY); if (inputfd < 0) ok = false; }
    for (int i = 0; ok && i < n; i++) {
#if defined(__COSMOPOLITAN__)
        if (km_windows_jobs_needed()) {
            p.stages[i].job = km_windows_job_create();
            if (p.stages[i].job < 0) { ok = false; break; }
            int input = i ? edges[(i - 1) * 2] : inputfd;
            int output = i == n - 1 ? (outputfd >= 0 ? outputfd : out[1]) : edges[i * 2 + 1];
            pid_t pid = -1;
            int error = km_spawn_graph_stage(&pid, argv[i], envp[i], cwd[i], input, output, err[1], out, err, execerr, edges, (n - 1) * 2, inputfd, outputfd);
            if (error) { km_windows_job_close(&p.stages[i].job); ok = false; break; }
            p.stages[i].pid = pid;
            p.stages[i].deadline = stage_timeouts[i] ? km_now() + stage_timeouts[i] : 0;
            setpgid(pid, pid);
            if (i == 0) p.pid = pid;
            // TODO(native-cli-review #9): posix_spawn already runs the native
            // executable before assignment, so it can create escaping children.
            // The fork ready-pipe barrier needs a Kame-controlled pre-exec child;
            // it cannot be added as a file action to an arbitrary executable.
            // Cosmocc 4.0.2 libc/proc/posix_spawn.h exposes neither suspended
            // creation nor a job attribute. A verified Windows suspended-launch
            // or equivalent job-at-creation seam is required; do not claim fixed.
            bool assigned = km_windows_job_assign(p.stages[i].job, pid);
            if (!assigned) {
                int status = 0;
                pid_t result = waitpid(pid, &status, WNOHANG);
                if (result == pid) {
                    p.stages[i].reaped = true;
                    if (WIFEXITED(status)) p.stages[i].status = WEXITSTATUS(status);
                    else if (WIFSIGNALED(status)) p.stages[i].signal = WTERMSIG(status);
                    assigned = true;
                } else {
                    kill(pid, SIGKILL);
                    do { result = waitpid(pid, &status, 0); } while (result < 0 && errno == EINTR);
                    p.stages[i].reaped = result == pid;
                }
            }
            if (!assigned) { ok = false; break; }
            p.stages[i].spawned_in_job = true;
            continue;
        }
#endif
        int ready[2] = {-1, -1};
        pid_t pid = fork();
        if (pid < 0) { km_close(&ready[0]); km_close(&ready[1]); ok = false; break; }
        if (pid == 0) {
            if (ready[0] >= 0) close(ready[1]);
            int input = topology ? -1 : i ? edges[(i - 1) * 2] : (inputfd >= 0 ? inputfd : open("/dev/null", O_RDONLY));
            int output = !topology && i != n - 1 ? edges[i * 2 + 1] : (outputfd >= 0 ? outputfd : out[1]);
            if (topology) {
                input = stage_inputs[i] >= 0 ? stage_inputs[i] : open("/dev/null", O_RDONLY);
                output = stage_outputs[i] >= 0 ? stage_outputs[i] : out[1];
                for (int j = 0; !node_kinds[i] && j < edge_count; j++) {
                    if (links[j * 2] == i) output = edges[j * 2 + 1];
                    if (links[j * 2 + 1] == i && !ports[j]) { if (stage_inputs[i] < 0 && input >= 0) close(input); input = edges[j * 2]; }
                }
                for (int j = 0; j < edge_count; j++) if (ports[j] && links[j * 2 + 1] == i && fcntl(operand_fds[j], F_SETFD, 0) < 0) { int error = errno; write(execerr[1], &error, sizeof(error)); _exit(127); }
            }
            if (setpgid(0, 0) || input < 0 || dup2(input, STDIN_FILENO) < 0 || dup2(output, STDOUT_FILENO) < 0 || dup2(err[1], STDERR_FILENO) < 0 || chdir(cwd[i]) < 0) {
                int error = errno; write(execerr[1], &error, sizeof(error)); _exit(127);
            }
            if ((i == 0 || topology) && input != STDIN_FILENO) close(input);
            if (i != 0 && inputfd >= 0) close(inputfd);
            if (outputfd >= 0) close(outputfd);
            if (topology) for (int j = 0; j < n; j++) { if (stage_inputs[j] >= 0 && stage_inputs[j] != input) close(stage_inputs[j]); if (stage_outputs[j] >= 0) close(stage_outputs[j]); }
            if (!topology || !node_kinds[i]) for (int j = 0; j < edge_count * 2; j++) close(edges[j]);
            close(out[0]); close(out[1]); close(err[0]); close(err[1]); close(execerr[0]);
            if (topology && node_kinds[i]) {
                close(execerr[1]);
                if ((node_kinds[i] & 15) == 8) _exit(km_run_each(i, edges, links, edge_count, argv[i], envp[i], p.metrics, p.live_states, p.live_states + n + i, collect_failures));
                _exit(km_run_mux(i, (int)node_kinds[i], edges, links, edge_count, (int)((so_int *)item_limits.ptr)[i], p.metrics, p.live_states, id, stream_item, stream_context));
            }
            if (ready[0] >= 0) {
                char token = 0;
                ssize_t nread;
                do { nread = read(ready[0], &token, 1); } while (nread < 0 && errno == EINTR);
                close(ready[0]);
                if (nread != 1 || token != 1) _exit(126);
            }
            km_exec_argv(argv[i], envp[i]);
            int error = errno; write(execerr[1], &error, sizeof(error)); _exit(127);
        }
        p.stages[i].pid = pid;
        p.stages[i].deadline = stage_timeouts[i] ? km_now() + stage_timeouts[i] : 0;
        setpgid(pid, pid);
        if (i == 0) p.pid = pid;
        if (ready[0] >= 0) {
            km_close(&ready[0]);
            bool assigned = km_windows_job_assign(p.stages[i].job, pid);
            if (assigned) {
                p.stages[i].spawned_in_job = true;
                char token = 1;
                ssize_t written;
                do { written = write(ready[1], &token, 1); } while (written < 0 && errno == EINTR);
                if (written != 1) assigned = false;
            }
            km_close(&ready[1]);
            if (!assigned) { ok = false; break; }
        }
    }
    if (argv) { for (int i = 0; i < n; i++) km_free_strings(argv[i], (int)counts[i]); free(argv); }
    if (envp) { for (int i = 0; i < n; i++) km_free_strings(envp[i], (int)envcounts[i]); free(envp); }
    km_free_strings(cwd, n); free(input_name); free(output_name);
    km_close(&inputfd); km_close(&outputfd);
    if (edges) { for (int i = 0; i < edge_count * 2; i++) km_close(&edges[i]); free(edges); }
    if (operand_fds) { for (int i = 0; i < edge_count; i++) km_close(&operand_fds[i]); free(operand_fds); }
    if (stage_inputs) { for (int i = 0; i < n; i++) km_close(&stage_inputs[i]); free(stage_inputs); }
    if (stage_outputs) { for (int i = 0; i < n; i++) km_close(&stage_outputs[i]); free(stage_outputs); }
    free(truncate_outputs);
    km_close(&out[1]); km_close(&err[1]); km_close(&execerr[1]);
    if (!ok) {
        if (p.stages) { km_signal(&p, SIGKILL); km_wait_graph(&p); }
        if (p.stages) for (int i = 0; i < n; i++) km_windows_job_close(&p.stages[i].job);
        free(p.stages); km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]);
        if (p.metrics) munmap(p.metrics, (size_t)p.metric_count * KM_METRIC_FIELDS * sizeof(int64_t));
        if (p.live_states) munmap(p.live_states, (size_t)n * 2 * sizeof(int));
        return km_spawn_failed(host, id, "graph spawn failed");
    }
    p.outfd = out[0]; p.errfd = err[0]; p.execfd = execerr[0]; p.deadline = timeout ? km_now() + timeout : 0;
    host->processes[host->len++] = p;
    return 0;
}

int km_event_stage_field(km_event *event, int index, int field) {
    if (!event || index < 0 || index >= event->stageCount || field < 0 || field > 2) return 0;
    return event->stageData[index * 3 + field];
}

int64_t km_event_metric_field(km_event *event, int index, int field) {
    if (!event || index < 0 || index >= event->metricCount || field < 0 || field >= KM_METRIC_FIELDS) return 0;
    return event->metricData[index * KM_METRIC_FIELDS + field];
}

static void km_exec_ready(km_host *host, km_process *p) {
    int child_errno;
    ssize_t n = read(p->execfd, &child_errno, sizeof(child_errno));
    if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)) return;
    if (n == 0) {
        if (!km_close_checked(&p->execfd)) { km_fail(host, p, "exec pipe close failed"); return; }
        if (km_windows_jobs_needed()) {
            bool assigned = true;
            if (p->stage_count) {
                for (int i = 0; assigned && i < p->stage_count; i++) if (!p->stages[i].spawned_in_job) assigned = km_windows_job_assign_exec(p->stages[i].job, p->stages[i].pid);
            } else assigned = km_windows_job_assign_exec(p->job, p->pid);
            if (!assigned) { km_fail(host, p, "Windows process job setup failed"); return; }
        }
        p->started = true;
        km_push(host, p, (km_event){.kind = KM_STARTED, .id = p->id, .pid = p->pid, .pgid = p->pid});
        return;
    }
    km_close(&p->execfd); km_close(&p->outfd); km_close(&p->errfd);
    if (p->stage_count) { km_fail(host, p, "spawn failed"); return; }
    pid_t result;
    do { result = waitpid(p->pid, NULL, 0); } while (result < 0 && errno == EINTR);
    if (result != p->pid) { km_fail(host, p, "waitpid failed"); return; }
    p->reaped = p->terminal = true;
    km_event event = {.kind = KM_TERMINAL, .id = p->id, .outcome = KM_FAILED};
    km_diagnostic(&event, KM_HOSTF, "spawn failed");
    km_push(host, p, event);
}

static void km_terminate(km_host *host, km_process *p, int outcome, int64_t grace_ms);

static void km_read(km_host *host, km_process *p, int *fd, int kind) {
    if (p->terminal || *fd < 0 || p->queued >= KM_QUEUE_LIMIT) return;
    int want = KM_QUEUE_LIMIT - p->queued; if (want > KM_CHUNK) want = KM_CHUNK;
    so_byte buffer[KM_CHUNK]; ssize_t n = read(*fd, buffer, (size_t)want);
    if (n == 0) { if (!km_close_checked(fd)) km_fail(host, p, "pipe close failed"); return; }
    if (n < 0) { if (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR) km_fail(host, p, "pipe read failed"); return; }
    km_event event = {.kind = kind, .id = p->id, .pid = p->pid, .pgid = p->pid, .data = km_copy(buffer, (int)n), .dataLen = (int)n};
    if (!km_push(host, p, event)) return;
    if (kind == KM_STDOUT) { if (!km_append(&p->stdout_data, &p->stdout_len, &p->stdout_cap, buffer, (int)n, p->retain, &p->stdout_truncated)) km_fail(host, p, "retained output allocation failed"); }
    else if (!km_append(&p->stderr_data, &p->stderr_len, &p->stderr_cap, buffer, (int)n, p->retain, &p->stderr_truncated)) km_fail(host, p, "retained output allocation failed");
    if (p->direct && p->retain > 0 && p->stdout_truncated) km_terminate(host, p, KM_CANCELLED, KM_GRACE_MS);
}

// Observe native POSIX and APE Linux exit without consuming the child: its
// waitable PID pins the group until draining and descendant cleanup finish.
// Windows instead pins ownership with a job handle and uses ordinary waitpid.
static int km_observe_child(pid_t pid, bool *exited, bool *reaped, int *code, int *signal_number) {
    if (*reaped) { *exited = true; return 0; }
    if (*exited) return 0;
    bool consume = km_windows_jobs_needed();
#if defined(__COSMOPOLITAN__)
    // ponytail: APE non-Linux POSIX keeps legacy reaped-leader behavior. Safe
    // non-consuming exit observation and descendant retirement remain deferred
    // there; never signal a reaped POSIX PID/group that may have been reused.
    consume = consume || !IsLinux();
#endif
    if (consume) {
        int status; pid_t result = waitpid(pid, &status, WNOHANG);
        if (result == 0 || (result < 0 && errno == EINTR)) return 0;
        if (result < 0) { if (errno == ECHILD) *reaped = true; return -1; }
        *exited = *reaped = true;
        if (WIFEXITED(status)) *code = WEXITSTATUS(status);
        else if (WIFSIGNALED(status)) *signal_number = WTERMSIG(status);
        return 0;
    }
    siginfo_t info;
    memset(&info, 0, sizeof(info));
    int result;
#if defined(__COSMOPOLITAN__)
    // Linux P_PID=1; Cosmopolitan's siginfo_t is the Linux syscall layout.
    result = sys_waitid(1, pid, &info, WEXITED | WNOHANG | WNOWAIT, NULL);
#else
    result = waitid(P_PID, pid, &info, WEXITED | WNOHANG | WNOWAIT);
#endif
    if (result < 0) {
        if (errno == EINTR) return 0;
        if (errno == ECHILD) *reaped = true;
        return -1;
    }
    if (!info.si_pid) return 0;
    *exited = true;
    // OpenBSD waitid retains the full exit(int); match waitpid's exit byte.
    if (info.si_code == CLD_EXITED) *code = info.si_status & 0xff;
    else *signal_number = info.si_status;
    return 0;
}

static void km_reap(km_host *host, km_process *p) {
    if (p->exited || p->terminal) return;
    if (host->force_waitpid_failure) {
        host->force_waitpid_failure = false;
        km_fail(host, p, "waitpid failed");
        return;
    }
    if (p->stage_count) {
        bool all = true;
        for (int i = 0; i < p->stage_count; i++) {
            km_stage *s = &p->stages[i];
            if (km_observe_child(s->pid, &s->exited, &s->reaped, &s->status, &s->signal) < 0) { km_fail(host, p, "waitpid failed"); return; }
            if (s->operator_kind == 8 && s->exited && p->live_states && __atomic_load_n(p->live_states + p->stage_count + i, __ATOMIC_RELAXED)) { km_fail(host, p, "per-record worker launch or transport failed"); return; }
            if (p->topology && !p->terminating && p->failed_stage < 0 && s->exited && (s->status || s->signal) && (!p->collect_failures || (s->operator_kind && s->operator_kind != 8) || s->signal)) {
                p->failed_stage = i;
                p->status = s->signal ? 128 + s->signal : s->status; p->signal = s->signal;
                km_signal(p, SIGKILL);
            }
            // A framing/predicate failure can close upstream before the parent
            // observes its exit. Preserve the causal operator diagnostic over
            // a resulting SIGPIPE, never reinterpret an executable's exit code.
            if (p->topology && !p->terminating && s->operator_kind && s->operator_kind != 8 && s->exited && !s->signal && ((s->status >= 120 && s->status <= 124) || s->status == KM_BUFFER_LIMIT)) {
                p->failed_stage = i; p->status = s->status; p->signal = 0;
            }
            if (!s->exited) all = false;
        }
        p->exited = all;
        if (all && (!p->topology || p->failed_stage < 0)) for (int i = 0; i < p->stage_count; i++) {
            km_stage *s = &p->stages[i];
            if (s->status || s->signal) { p->status = s->signal ? 128 + s->signal : s->status; p->signal = s->signal; }
        }
        return;
    }
    if (km_observe_child(p->pid, &p->exited, &p->reaped, &p->status, &p->signal) < 0) km_fail(host, p, "waitpid failed");
}

static void km_terminate(km_host *host, km_process *p, int outcome, int64_t grace_ms) {
    if (p->terminal || p->terminating) return;
    for (int i = 0; i < p->stage_count; i++) if (!p->stages[i].exited && !p->stages[i].reaped) p->stages[i].outcome = outcome;
    if (km_signal(p, SIGTERM) < 0) { km_fail(host, p, "SIGTERM failed"); return; }
    if (grace_ms < 0) grace_ms = 0;
    p->terminating = true; p->outcome = outcome; p->kill_deadline = km_now() + grace_ms;
}

int km_host_cancel(km_host *host, int64_t id, bool timeout) {
    if (!host) return -1;
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) { km_terminate(host, &host->processes[i], timeout ? KM_TIMED_OUT : KM_CANCELLED, KM_GRACE_MS); return 0; }
    return -1;
}

int km_host_stop(km_host *host, int64_t id, int64_t grace_ms) {
    if (!host) return -1;
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) { km_terminate(host, &host->processes[i], KM_CANCELLED, grace_ms); return 0; }
    return -1;
}

void km_host_cancel_all(km_host *host) {
    if (!host) return;
    for (int i = 0; i < host->len; i++) km_terminate(host, &host->processes[i], KM_CANCELLED, KM_GRACE_MS);
}

static void km_snapshot(km_host *host, km_process *p, int64_t now) {
    if (!p->live_states || !p->started || p->terminal || now < p->snapshot_at) return;
    p->snapshot_at = now + 100;
    km_event event = {.kind = KM_SNAPSHOT, .id = p->id};
    event.stageData = km_event_allocate() ? calloc((size_t)p->stage_count * 3, sizeof(int)) : NULL;
    if (!event.stageData) return;
    event.stageCount = p->stage_count;
    for (int i = 0; i < p->stage_count; i++) {
        km_stage *stage = &p->stages[i];
        int state = __atomic_load_n(p->live_states + i, __ATOMIC_RELAXED);
        if (stage->exited) {
            state = stage->status || stage->signal || stage->outcome ? KM_STAGE_FAILED : KM_COMPLETED;
            if (stage->outcome == KM_CANCELLED) state = KM_STAGE_CANCELLED;
            if (stage->outcome == KM_TIMED_OUT) state = KM_STAGE_TIMEOUT;
        }
        event.stageData[i * 3] = state;
    }
    if (p->metrics) {
        size_t size = (size_t)p->metric_count * KM_METRIC_FIELDS * sizeof(int64_t);
        event.metricData = km_event_allocate() ? malloc(size) : NULL;
        if (event.metricData) {
            event.metricCount = p->metric_count;
            for (int i = 0; i < p->metric_count * KM_METRIC_FIELDS; i++) event.metricData[i] = __atomic_load_n(p->metrics + i, __ATOMIC_RELAXED);
        }
    }
    km_push(host, p, event);
}

int km_host_pump(km_host *host, int wait) {
    if (!host) return -1;
    int64_t now = km_now();
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (!p->terminal && p->stage_count) km_reap(host, p);
        for (int j = 0; !p->terminal && !p->terminating && j < p->stage_count; j++) {
            km_stage *stage = &p->stages[j];
            if (!stage->exited && !stage->reaped && stage->deadline && now >= stage->deadline) km_terminate(host, p, KM_TIMED_OUT, KM_GRACE_MS);
        }
        if (!p->terminal && p->deadline && now >= p->deadline) km_terminate(host, p, KM_TIMED_OUT, KM_GRACE_MS);
        if (!p->terminal && p->terminating && !p->killed && now >= p->kill_deadline) { if (km_signal(p, SIGKILL) < 0) km_fail(host, p, "SIGKILL failed"); p->killed = true; }
    }
    int poll_wait = wait < 0 ? 0 : wait;
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (p->live_states && !p->terminal && poll_wait > 100) poll_wait = 100;
        int64_t due = 0;
        if (!p->terminal && !p->terminating && p->deadline) due = p->deadline;
        for (int j = 0; !p->terminal && !p->terminating && j < p->stage_count; j++) {
            km_stage *stage = &p->stages[j];
            if (!stage->exited && !stage->reaped && stage->deadline && (!due || stage->deadline < due)) due = stage->deadline;
        }
        if (!p->terminal && p->terminating && !p->killed && (!due || p->kill_deadline < due)) due = p->kill_deadline;
        if (due) {
            int64_t remaining = due - now;
            if (remaining < 0) remaining = 0;
            if (remaining < poll_wait) poll_wait = (int)remaining;
        }
    }
    struct pollfd *fds = calloc((size_t)host->len * 3, sizeof(*fds));
    km_process **owners = calloc((size_t)host->len * 3, sizeof(*owners));
    int *kinds = calloc((size_t)host->len * 3, sizeof(*kinds)); int count = 0;
    if ((!fds || !owners || !kinds) && host->len) {
        free(fds); free(owners); free(kinds);
        for (int i = 0; i < host->len; i++) km_fail(host, &host->processes[i], "poll allocation failed");
        return -1;
    }
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (!p->terminal && p->execfd >= 0) { fds[count].fd = p->execfd; fds[count].events = POLLIN|POLLHUP; owners[count] = p; kinds[count++] = 99; }
        if (!p->terminal && p->started && p->queued < KM_QUEUE_LIMIT && p->outfd >= 0) { fds[count].fd = p->outfd; fds[count].events = POLLIN|POLLHUP; owners[count] = p; kinds[count++] = KM_STDOUT; }
        if (!p->terminal && p->started && p->queued < KM_QUEUE_LIMIT && p->errfd >= 0) { fds[count].fd = p->errfd; fds[count].events = POLLIN|POLLHUP; owners[count] = p; kinds[count++] = KM_STDERR; }
    }
    int ready; do { ready = poll(fds, (nfds_t)count, poll_wait); } while (ready < 0 && errno == EINTR);
    if (ready < 0) {
        free(fds); free(owners); free(kinds);
        for (int i = 0; i < host->len; i++) km_fail(host, &host->processes[i], "poll failed");
        return -1;
    }
    for (int i = 0; i < count; i++) if (fds[i].revents) {
        if (kinds[i] == 99) km_exec_ready(host, owners[i]);
        else km_read(host, owners[i], kinds[i] == KM_STDOUT ? &owners[i]->outfd : &owners[i]->errfd, kinds[i]);
    }
    free(fds); free(owners); free(kinds);
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        km_reap(host, p);
        km_snapshot(host, p, km_now());
        if (!p->terminal && p->exited && p->outfd < 0 && p->errfd < 0) {
            // Descendants that closed captured streams still belong to this
            // request. Retire them while the POSIX leader PID is still pinned.
            if (km_signal(p, SIGKILL) < 0) { km_fail(host, p, "SIGKILL failed"); continue; }
            bool waited = p->stage_count ? km_wait_graph(p) : km_wait_child(p->pid, &p->reaped, &p->status, &p->signal);
            if (!waited) km_fail(host, p, "waitpid failed");
            else km_emit_terminal(host, p, p->terminating ? p->outcome : KM_EXITED, NULL);
        }
    }
    return 0;
}

bool km_host_next(km_host *host, km_event *event) {
    if (!host) return false;
    uint64_t sequence = host->first ? host->first->sequence : UINT64_MAX;
    km_event *pending = NULL;
    uint64_t *pending_sequence = NULL;
    int64_t process_id = 0;
    if (host->rejected_sequence && host->rejected_sequence < sequence) {
        pending = &host->rejected; pending_sequence = &host->rejected_sequence; sequence = *pending_sequence;
    }
    for (int i = 0; i < host->len; i++) for (int j = 0; j < 2; j++) {
        km_process *p = &host->processes[i];
        if (p->pending_sequence[j] && p->pending_sequence[j] < sequence) {
            pending = &p->pending[j]; pending_sequence = &p->pending_sequence[j];
            sequence = *pending_sequence; process_id = p->id;
        }
    }
    if (pending) {
        *event = *pending; memset(pending, 0, sizeof(*pending)); *pending_sequence = 0;
        if (event->kind == KM_TERMINAL && process_id) km_remove_process(host, process_id);
        return true;
    }
    if (!host->first) return false;
    km_event_node *node = host->first; host->first = node->next; if (!host->first) host->last = NULL;
    *event = node->event;
    km_process *process = node->process_id ? km_process_for(host, node->process_id) : NULL;
    if (process && (event->kind == KM_STDOUT || event->kind == KM_STDERR)) process->queued -= event->dataLen;
    if (event->kind == KM_TERMINAL) km_remove_process(host, node->process_id);
    free(node); return true;
}

int km_host_active(km_host *host) {
    int active = 0;
    if (!host) return 0;
    for (int i = 0; i < host->len; i++) if (host->processes[i].started && !host->processes[i].terminal) active++;
    return active;
}

void km_host_force_waitpid_failure(km_host *host) {
    if (host) host->force_waitpid_failure = true;
}

void km_host_free(km_host *host) {
    if (!host) return;
    bool terminating = false;
    for (int i = 0; i < host->len; i++) {
        km_signal(&host->processes[i], SIGTERM);
        terminating = true;
    }
    if (terminating) poll(NULL, 0, KM_GRACE_MS);
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        km_signal(p, SIGKILL);
        if (!p->reaped) {
            if (p->stage_count) km_wait_graph(p);
            else km_wait_child(p->pid, &p->reaped, &p->status, &p->signal);
        }
        km_release_process(p);
    }
    for (int i = 0; i < 256; i++) {
        while (host->cache_lock_refs[i] != 0 && km_cache_lock_refs[i] != 0) km_host_cache_unlock(host, i);
    }
    free(host->processes);
    if (host->rejected_sequence) km_free_event(&host->rejected);
    while (host->first) { km_event_node *node = host->first; host->first = node->next; km_free_event(&node->event); free(node); }
    free(host);
}
