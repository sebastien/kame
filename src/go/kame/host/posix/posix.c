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
#include <sys/wait.h>
#include <signal.h>
#if defined(__COSMOPOLITAN__)
#include <spawn.h>
#endif
#include <time.h>
#include <unistd.h>
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
#endif

// Event kind and outcome values must match posix.EventKind and posix.Outcome.
enum { KM_STARTED, KM_STDOUT, KM_STDERR, KM_TERMINAL };
enum { KM_EXITED, KM_TIMED_OUT, KM_CANCELLED, KM_FAILED };
enum { KM_QUEUE_LIMIT = 256 * 1024, KM_CHUNK = 16 * 1024, KM_GRACE_MS = 100 };

// Host diagnostics use stable codes from docs/spec/011-diagnostics.md.
#define KM_HOSTF "HOST_FAIL"

typedef struct km_stage {
    pid_t pid;
    int64_t job;
    int64_t deadline;
    int status, signal, outcome;
    bool reaped, spawned_in_job;
} km_stage;

typedef struct km_process {
    int64_t id;
    pid_t pid;
    int64_t job;
    int outfd, errfd, execfd;
    bool started, reaped, terminating, killed, terminal;
    int outcome, status, signal;
    int64_t deadline, kill_deadline;
    int queued;
    so_byte *stdout_data, *stderr_data;
    int stdout_len, stderr_len, retain, stdout_cap, stderr_cap;
    bool stdout_truncated, stderr_truncated;
    bool direct;
    km_stage *stages;
    int stage_count;
} km_process;

typedef struct km_event_node {
    km_event event;
    int64_t process_id;
    struct km_event_node *next;
} km_event_node;

struct km_host {
    km_process *processes;
    int len, cap;
    km_event_node *first, *last;
    bool force_waitpid_failure;
    unsigned cache_lock_refs[256];
};

static int km_cache_lock_fd[256];
#if defined(__COSMOPOLITAN__)
static int64_t km_cache_lock_mutex[256];
#endif
static unsigned km_cache_lock_refs[256];
static bool km_cache_lock_initialized;

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

static bool km_windows_cache_lock(int stripe) {
    intptr_t kernel = GetModuleHandle("kernel32.dll");
    if (!kernel) return false;
    if (!km_create_mutex) km_create_mutex = (km_create_mutex_fn)GetProcAddress(kernel, "CreateMutexW");
    if (!km_wait_single_object) km_wait_single_object = (km_wait_single_object_fn)GetProcAddress(kernel, "WaitForSingleObject");
    if (!km_release_mutex) km_release_mutex = (km_release_mutex_fn)GetProcAddress(kernel, "ReleaseMutex");
    if (!km_create_mutex || !km_wait_single_object || !km_release_mutex) return false;
    char16_t name[48] = {'L','o','c','a','l','\\','K','a','m','e','C','a','c','h','e','L','o','c','k','_'};
    int digits[3] = {stripe / 100, (stripe / 10) % 10, stripe % 10};
    int at = 20;
    if (digits[0]) name[at++] = (char16_t)('0' + digits[0]);
    if (digits[0] || digits[1]) name[at++] = (char16_t)('0' + digits[1]);
    name[at++] = (char16_t)('0' + digits[2]);
    name[at] = 0;
    int64_t mutex = km_create_mutex(NULL, 0, name);
    if (!mutex || mutex == -1) return false;
    uint32_t result = km_wait_single_object(mutex, UINT32_MAX);
    if (result != 0 && result != 0x80) { CloseHandle(mutex); return false; }
    km_cache_lock_mutex[stripe] = mutex;
    return true;
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
static bool km_windows_job_assign_exec(int64_t job, pid_t pid) {
    if (!km_windows_jobs_needed()) return true;
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
static bool km_windows_cache_lock(int stripe) { (void)stripe; return false; }
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

static int64_t km_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static void km_free_event(km_event *event) {
    free(event->data); free(event->diagnosticCode); free(event->diagnostic);
    free(event->output); free(event->errorOutput);
    free(event->stageData);
    memset(event, 0, sizeof(*event));
}

void km_event_free(km_event *event) { km_free_event(event); }

static bool km_push(km_host *host, km_process *process, km_event event) {
    km_event_node *node = calloc(1, sizeof(*node));
    if (!node) { km_free_event(&event); return false; }
    node->event = event; node->process_id = process ? process->id : 0;
    if (host->last) host->last->next = node; else host->first = node;
    host->last = node;
    return true;
}

static so_byte *km_copy(const void *data, int len) {
    if (len <= 0) return NULL;
    so_byte *out = malloc((size_t)len);
    if (out) memcpy(out, data, (size_t)len);
    return out;
}

static char *km_cstring(so_String value) {
    if (value.len < 0 || (value.len && (!value.ptr || memchr(value.ptr, '\0', (size_t)value.len)))) return NULL;
    char *out = calloc((size_t)value.len + 1, 1);
    if (out && value.len) memcpy(out, value.ptr, (size_t)value.len);
    return out;
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
    memset(p, 0, sizeof(*p)); p->outfd = p->errfd = p->execfd = -1;
}

static bool km_append(so_byte **data, int *len, int *cap, const so_byte *chunk, int n, int limit, bool *truncated) {
    if (*len >= limit) { *truncated = true; return true; }
    if (n > limit - *len) { n = limit - *len; *truncated = true; }
    if (*len + n > *cap) {
        int next = *cap ? *cap : 64;
        while (next < *len + n) next *= 2;
        so_byte *grown = realloc(*data, (size_t)next);
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
        event.stageData = calloc((size_t)p->stage_count * 3, sizeof(int));
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
            return kill(p->pid, sig) < 0 && errno != ESRCH ? -1 : 0;
        }
        return kill(-p->pid, sig) < 0 && errno != ESRCH ? -1 : 0;
    }
    for (int i = 0; i < p->stage_count; i++) if (p->stages[i].pid > 0) {
        if (km_windows_jobs_needed() && p->stages[i].job) {
            if (sig == SIGKILL) { if (!km_windows_job_terminate(p->stages[i].job, sig)) failed = -1; }
            else if (kill(p->stages[i].pid, sig) < 0 && errno != ESRCH) failed = -1;
        } else if (kill(-p->stages[i].pid, sig) < 0 && errno != ESRCH) failed = -1;
    }
    return failed;
}

static void km_wait_graph(km_process *p) {
    for (int i = 0; i < p->stage_count; i++) {
        km_stage *s = &p->stages[i];
        if (s->pid <= 0 || s->reaped) continue;
        int status; pid_t result;
        do { result = waitpid(s->pid, &status, 0); } while (result < 0 && errno == EINTR);
        s->reaped = true;
        if (result == s->pid) {
            if (WIFEXITED(status)) s->status = WEXITSTATUS(status);
            else if (WIFSIGNALED(status)) s->signal = WTERMSIG(status);
        }
    }
    p->reaped = true;
}

static void km_fail(km_host *host, km_process *p, const char *diagnostic) {
    if (p->stage_count) {
        for (int i = 0; i < p->stage_count; i++) if (!p->stages[i].reaped && !p->terminating) p->stages[i].outcome = KM_FAILED;
        km_signal(p, SIGKILL); km_wait_graph(p);
    }
    if (!p->reaped) km_signal(p, SIGKILL);
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

int km_host_cache_lock(km_host *host, so_String name, int stripe) {
    if (!host || stripe < 0 || stripe >= 256) return -1;
    if (km_cache_lock_refs[stripe] != 0) {
        host->cache_lock_refs[stripe]++;
        km_cache_lock_refs[stripe]++;
        return 0;
    }
    if (km_windows_jobs_needed()) {
        if (!km_windows_cache_lock(stripe)) return -1;
        host->cache_lock_refs[stripe] = 1;
        km_cache_lock_refs[stripe] = 1;
        return 0;
    }
    char *path = km_cstring(name);
    if (!path) return -1;
    int fd = open(path, O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
    free(path);
    if (fd < 0) return -1;
    while (flock(fd, LOCK_EX) != 0) {
        if (errno == EINTR) continue;
        close(fd);
        return -1;
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
    if (fd >= 0) { flock(fd, LOCK_UN); close(fd); }
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

// Unlike execvp, never fall back to /bin/sh for an ENOEXEC file.
static void km_exec_argv(char **argv, char **envp) {
    if (strchr(argv[0], '/')) { execve(argv[0], argv, envp); return; }
    const char *paths = "/bin:/usr/bin";
    for (char **entry = envp; *entry; entry++) if (strncmp(*entry, "PATH=", 5) == 0) { paths = *entry + 5; break; }
    int saved = ENOENT;
    const char *part = paths;
    for (;;) {
        const char *end = strchr(part, ':');
        size_t n = end ? (size_t)(end - part) : strlen(part);
        size_t name_len = strlen(argv[0]);
        char *path = malloc(n + name_len + 2);
        if (!path) { errno = ENOMEM; return; }
        if (n) { memcpy(path, part, n); path[n] = '/'; memcpy(path + n + 1, argv[0], name_len + 1); }
        else memcpy(path, argv[0], name_len + 1);
        execve(path, argv, envp);
        int error = errno;
        free(path);
        if (error != ENOENT && error != ENOTDIR && error != EACCES) { errno = error; return; }
        if (error == EACCES) saved = EACCES;
        if (!end) break;
        part = end + 1;
    }
    errno = saved;
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

static int km_spawn_argv(pid_t *pid, char **argv, char **envp, posix_spawn_file_actions_t *actions, posix_spawnattr_t *attr) {
    if (strchr(argv[0], '/')) return posix_spawn(pid, argv[0], actions, attr, argv, envp);
    const char *paths = "/bin:/usr/bin";
    for (char **entry = envp; *entry; entry++) if (strncmp(*entry, "PATH=", 5) == 0) { paths = *entry + 5; break; }
    int saved = ENOENT;
    const char *part = paths;
    for (;;) {
        const char *end = strchr(part, ':');
        size_t n = end ? (size_t)(end - part) : strlen(part);
        size_t name_len = strlen(argv[0]);
        char *path = malloc(n + name_len + 2);
        if (!path) return ENOMEM;
        if (n) { memcpy(path, part, n); path[n] = '/'; memcpy(path + n + 1, argv[0], name_len + 1); }
        else memcpy(path, argv[0], name_len + 1);
        int error = posix_spawn(pid, path, actions, attr, argv, envp);
        free(path);
        if (!error) return 0;
        if (error != ENOENT && error != ENOTDIR && error != EACCES) return error;
        if (error == EACCES) saved = EACCES;
        if (!end) return saved;
        part = end + 1;
    }
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
    if (!error) error = km_spawn_argv(pid, argv, envp, &actions, &attr);
done:
    if (trace) fprintf(stderr, "[windows-spawn] done argv=%s error=%d pid=%d\n", argv[0], error, (int)*pid);
    posix_spawnattr_destroy(&attr);
    posix_spawn_file_actions_destroy(&actions);
    return error;
}
#endif

int km_host_start(km_host *host, int64_t id, so_Slice shell, so_Slice script, so_String directory, so_Slice environment, int64_t timeout, so_int retain, bool direct) {
    if (!host) return -1;
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
    if (!host) return -1;
    if (!id || lengths.len < 1 || lengths.len > INT_MAX / 2 || timeout < 0 || retain < 0) return km_spawn_failed(host, id, "invalid graph request");
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return km_spawn_failed(host, id, "request ID already active");
    so_int *counts = lengths.ptr;
    so_int total = 0;
    for (int i = 0; i < lengths.len; i++) {
        if (counts[i] <= 0 || counts[i] > arguments.len - total) return km_spawn_failed(host, id, "invalid graph argv");
        total += counts[i];
    }
    if (total != arguments.len) return km_spawn_failed(host, id, "invalid graph argv");
    int n = (int)lengths.len;
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
    int *edges = malloc((size_t)(n > 1 ? n - 1 : 1) * 2 * sizeof(int));
    int inputfd = -1, outputfd = -1;
    char *input_name = km_cstring(input_path), *output_name = km_cstring(output_path);
    char ***argv = calloc((size_t)n, sizeof(char **));
    char ***envp = calloc((size_t)n, sizeof(char **));
    char **cwd = calloc((size_t)n, sizeof(char *));
    km_process p = {.id = id, .outfd = -1, .errfd = -1, .execfd = -1, .direct = true, .retain = (int)retain, .stage_count = n};
    p.stages = calloc((size_t)n, sizeof(km_stage));
    if (edges) for (int i = 0; i < (n - 1) * 2; i++) edges[i] = -1;
    bool ok = edges && argv && envp && cwd && p.stages && input_name && output_name;
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
    for (int i = 0; ok && i < n - 1; i++) {
        int pipe_status = km_windows_jobs_needed() ? km_windows_sync_pipe(edges + i * 2) : pipe(edges + i * 2);
        if (pipe_status) ok = false;
    }
    // Exec closes every unused descriptor, including the shared launch channel.
    for (int i = 0; ok && i < (n - 1) * 2; i++) if (fcntl(edges[i], F_SETFD, FD_CLOEXEC) < 0) ok = false;
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
    if (ok && output_name[0]) { outputfd = open(output_name, O_WRONLY | O_CREAT | (append_output ? O_APPEND : O_TRUNC), 0666); if (outputfd < 0) ok = false; }
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
            int input = i ? edges[(i - 1) * 2] : (inputfd >= 0 ? inputfd : open("/dev/null", O_RDONLY));
            int output = i == n - 1 ? (outputfd >= 0 ? outputfd : out[1]) : edges[i * 2 + 1];
            if (setpgid(0, 0) || input < 0 || dup2(input, STDIN_FILENO) < 0 || dup2(output, STDOUT_FILENO) < 0 || dup2(err[1], STDERR_FILENO) < 0 || chdir(cwd[i]) < 0) {
                int error = errno; write(execerr[1], &error, sizeof(error)); _exit(127);
            }
            if (i == 0 && input != STDIN_FILENO) close(input);
            if (i != 0 && inputfd >= 0) close(inputfd);
            if (outputfd >= 0) close(outputfd);
            for (int j = 0; j < (n - 1) * 2; j++) close(edges[j]);
            close(out[0]); close(out[1]); close(err[0]); close(err[1]); close(execerr[0]);
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
    if (edges) { for (int i = 0; i < (n - 1) * 2; i++) km_close(&edges[i]); free(edges); }
    km_close(&out[1]); km_close(&err[1]); km_close(&execerr[1]);
    if (!ok) {
        if (p.stages) { km_signal(&p, SIGKILL); km_wait_graph(&p); }
        if (p.stages) for (int i = 0; i < n; i++) km_windows_job_close(&p.stages[i].job);
        free(p.stages); km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]);
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
    if (*fd < 0 || p->queued >= KM_QUEUE_LIMIT) return;
    int want = KM_QUEUE_LIMIT - p->queued; if (want > KM_CHUNK) want = KM_CHUNK;
    so_byte buffer[KM_CHUNK]; ssize_t n = read(*fd, buffer, (size_t)want);
    if (n == 0) { if (!km_close_checked(fd)) km_fail(host, p, "pipe close failed"); return; }
    if (n < 0) { if (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR) km_fail(host, p, "pipe read failed"); return; }
    km_event event = {.kind = kind, .id = p->id, .pid = p->pid, .pgid = p->pid, .data = km_copy(buffer, (int)n), .dataLen = (int)n};
    if (!event.data || !km_push(host, p, event)) { km_fail(host, p, "output queue allocation failed"); return; }
    p->queued += (int)n;
    if (kind == KM_STDOUT) { if (!km_append(&p->stdout_data, &p->stdout_len, &p->stdout_cap, buffer, (int)n, p->retain, &p->stdout_truncated)) km_fail(host, p, "retained output allocation failed"); }
    else if (!km_append(&p->stderr_data, &p->stderr_len, &p->stderr_cap, buffer, (int)n, p->retain, &p->stderr_truncated)) km_fail(host, p, "retained output allocation failed");
    if (p->direct && p->retain > 0 && p->stdout_truncated) km_terminate(host, p, KM_CANCELLED, KM_GRACE_MS);
}

static void km_reap(km_host *host, km_process *p) {
    if (p->reaped || p->terminal) return;
    if (host->force_waitpid_failure) {
        host->force_waitpid_failure = false;
        km_fail(host, p, "waitpid failed");
        return;
    }
    if (p->stage_count) {
        bool all = true;
        for (int i = 0; i < p->stage_count; i++) {
            km_stage *s = &p->stages[i];
            if (s->reaped) continue;
            int status; pid_t result = waitpid(s->pid, &status, WNOHANG);
            if (result == 0 || (result < 0 && errno == EINTR)) { all = false; continue; }
            if (result < 0) { km_fail(host, p, "waitpid failed"); return; }
            s->reaped = true;
            if (WIFEXITED(status)) s->status = WEXITSTATUS(status);
            else if (WIFSIGNALED(status)) s->signal = WTERMSIG(status);
        }
        p->reaped = all;
        if (all) for (int i = 0; i < p->stage_count; i++) {
            km_stage *s = &p->stages[i];
            if (s->status || s->signal) { p->status = s->signal ? 128 + s->signal : s->status; p->signal = s->signal; }
        }
        return;
    }
    int status; pid_t result = waitpid(p->pid, &status, WNOHANG);
    if (result == 0) return;
    if (result < 0) { if (errno != EINTR) km_fail(host, p, "waitpid failed"); return; }
    p->reaped = true;
    if (WIFEXITED(status)) p->status = WEXITSTATUS(status); else if (WIFSIGNALED(status)) p->signal = WTERMSIG(status);
}

static void km_terminate(km_host *host, km_process *p, int outcome, int64_t grace_ms) {
    if (p->terminal || p->terminating) return;
    for (int i = 0; i < p->stage_count; i++) if (!p->stages[i].reaped) p->stages[i].outcome = outcome;
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

int km_host_pump(km_host *host, int wait) {
    if (!host) return -1;
    int64_t now = km_now();
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (!p->terminal && p->stage_count) km_reap(host, p);
        for (int j = 0; !p->terminal && !p->terminating && j < p->stage_count; j++) {
            km_stage *stage = &p->stages[j];
            if (!stage->reaped && stage->deadline && now >= stage->deadline) km_terminate(host, p, KM_TIMED_OUT, KM_GRACE_MS);
        }
        if (!p->terminal && p->deadline && now >= p->deadline) km_terminate(host, p, KM_TIMED_OUT, KM_GRACE_MS);
        if (!p->terminal && p->terminating && !p->killed && now >= p->kill_deadline) { if (km_signal(p, SIGKILL) < 0) km_fail(host, p, "SIGKILL failed"); p->killed = true; }
    }
    int poll_wait = wait < 0 ? 0 : wait;
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        int64_t due = 0;
        if (!p->terminal && !p->terminating && p->deadline) due = p->deadline;
        for (int j = 0; !p->terminal && !p->terminating && j < p->stage_count; j++) {
            km_stage *stage = &p->stages[j];
            if (!stage->reaped && stage->deadline && (!due || stage->deadline < due)) due = stage->deadline;
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
    for (int i = 0; i < host->len; i++) { km_process *p = &host->processes[i]; km_reap(host, p); if (!p->terminal && p->reaped && p->outfd < 0 && p->errfd < 0) km_emit_terminal(host, p, p->terminating ? p->outcome : KM_EXITED, NULL); }
    return 0;
}

bool km_host_next(km_host *host, km_event *event) {
    if (!host || !host->first) return false;
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
    for (int i = 0; i < host->len; i++) if (!host->processes[i].reaped) {
        km_signal(&host->processes[i], SIGTERM);
        terminating = true;
    }
    if (terminating) poll(NULL, 0, KM_GRACE_MS);
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (!p->reaped) {
            km_signal(p, SIGKILL);
            if (p->stage_count) km_wait_graph(p);
            else while (waitpid(p->pid, NULL, 0) < 0 && errno == EINTR) {}
        }
        km_release_process(p);
    }
    for (int i = 0; i < 256; i++) {
        while (host->cache_lock_refs[i] != 0 && km_cache_lock_refs[i] != 0) km_host_cache_unlock(host, i);
    }
    free(host->processes);
    while (host->first) { km_event_node *node = host->first; host->first = node->next; km_free_event(&node->event); free(node); }
    free(host);
}
