//go:build ignore
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <sys/ioctl.h>
#include <sys/wait.h>
#include <signal.h>
#include <time.h>
#include <unistd.h>

// Event kind and outcome values must match posix.EventKind and posix.Outcome.
enum { KM_STARTED, KM_STDOUT, KM_STDERR, KM_TERMINAL };
enum { KM_EXITED, KM_TIMED_OUT, KM_CANCELLED, KM_FAILED };
enum { KM_QUEUE_LIMIT = 256 * 1024, KM_CHUNK = 16 * 1024, KM_GRACE_MS = 100 };

// Host diagnostics use stable codes from docs/spec/011-diagnostics.md.
#define KM_HOSTF "HOST_FAIL"

typedef struct km_process {
    int64_t id;
    pid_t pid;
    int outfd, errfd, execfd;
    bool started, reaped, terminating, killed, terminal;
    int outcome, status, signal;
    int64_t deadline, kill_deadline;
    int queued;
    so_byte *stdout_data, *stderr_data;
    int stdout_len, stderr_len, retain, stdout_cap, stderr_cap;
    bool stdout_truncated, stderr_truncated;
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
};

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

static int64_t km_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static void km_free_event(km_event *event) {
    free(event->data); free(event->diagnosticCode); free(event->diagnostic);
    free(event->output); free(event->errorOutput);
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
    if (diagnostic) km_diagnostic(&event, KM_HOSTF, diagnostic);
    km_push(host, p, event);
}

static void km_fail(km_host *host, km_process *p, const char *diagnostic) {
    if (!p->reaped) kill(-p->pid, SIGKILL);
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

km_host *km_host_new(void) { return calloc(1, sizeof(km_host)); }

static int km_spawn_failed(km_host *host, int64_t id, const char *message) {
    km_event event = {.kind = KM_TERMINAL, .id = id, .outcome = KM_FAILED};
    km_diagnostic(&event, KM_HOSTF, message);
    km_push(host, NULL, event);
    return -1;
}

// A post-fork setup failure must not leave an untracked child behind. Retry an
// interrupted wait and let the terminal diagnostic distinguish failed cleanup
// from an ordinary spawn failure.
static bool km_abort_spawn(pid_t pid) {
    if (kill(-pid, SIGKILL) < 0 && errno != ESRCH) return false;
    pid_t result;
    do { result = waitpid(pid, NULL, 0); } while (result < 0 && errno == EINTR);
    return result == pid;
}

int km_host_start(km_host *host, int64_t id, so_Slice shell, so_Slice script, so_String directory, so_Slice environment, int64_t timeout, so_int retain) {
    if (!host) return -1;
    // Every rejected request emits exactly one failed terminal event.
    if (id == 0 || shell.len == 0 || timeout < 0 || retain < 0) return km_spawn_failed(host, id, "invalid request");
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return km_spawn_failed(host, id, "request ID already active");
    int out[2] = {-1, -1}, err[2] = {-1, -1}, execerr[2] = {-1, -1};
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
    argv[shell.len] = calloc((size_t)script.len + 1, 1);
    if (!argv[shell.len]) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    if (script.len) memcpy(argv[shell.len], script.ptr, (size_t)script.len);
    for (int i = 0; i < environment.len; i++) {
        envp[i] = km_cstring(envs[i]);
        if (!envp[i]) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    }
    pid_t pid = fork();
    if (pid < 0) { km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    if (pid == 0) {
        int child_errno;
        close(out[0]); close(err[0]); close(execerr[0]);
        if (setpgid(0, 0) || dup2(out[1], STDOUT_FILENO) < 0 || dup2(err[1], STDERR_FILENO) < 0 || chdir(cwd) < 0) {
            child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127);
        }
        close(out[1]); close(err[1]); execve(argv[0], argv, envp);
        child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127);
    }
    km_free_strings(argv, (int)shell.len + 1); km_free_strings(envp, (int)environment.len); free(cwd);
    close(out[1]); close(err[1]); close(execerr[1]); out[1] = err[1] = execerr[1] = -1;
    setpgid(pid, pid);
    if (km_set_nonblock(out[0]) || km_set_nonblock(err[0]) || km_set_nonblock(execerr[0])) { bool reaped = km_abort_spawn(pid); km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]); return km_spawn_failed(host, id, reaped ? "spawn failed" : "waitpid failed"); }
    if (host->len == host->cap) {
        int cap = host->cap ? host->cap * 2 : 4;
        km_process *processes = realloc(host->processes, (size_t)cap * sizeof(*processes));
        if (!processes) { bool reaped = km_abort_spawn(pid); km_close(&out[0]); km_close(&err[0]); km_close(&execerr[0]); return km_spawn_failed(host, id, reaped ? "spawn failed" : "waitpid failed"); }
        host->processes = processes; host->cap = cap;
    }
    km_process *p = &host->processes[host->len++];
    memset(p, 0, sizeof(*p)); p->id = id; p->pid = pid; p->outfd = out[0]; p->errfd = err[0]; p->execfd = execerr[0]; p->retain = retain; p->deadline = timeout ? km_now() + timeout : 0;
    return 0;
fail:
    km_close(&out[0]); km_close(&out[1]); km_close(&err[0]); km_close(&err[1]); km_close(&execerr[0]); km_close(&execerr[1]);
    return km_spawn_failed(host, id, "spawn failed");
}

static void km_exec_ready(km_host *host, km_process *p) {
    int child_errno;
    ssize_t n = read(p->execfd, &child_errno, sizeof(child_errno));
    if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)) return;
    if (n == 0) {
        if (!km_close_checked(&p->execfd)) { km_fail(host, p, "exec pipe close failed"); return; }
        p->started = true;
        km_push(host, p, (km_event){.kind = KM_STARTED, .id = p->id, .pid = p->pid, .pgid = p->pid});
        return;
    }
    km_close(&p->execfd); km_close(&p->outfd); km_close(&p->errfd);
    pid_t result;
    do { result = waitpid(p->pid, NULL, 0); } while (result < 0 && errno == EINTR);
    if (result != p->pid) { km_fail(host, p, "waitpid failed"); return; }
    p->reaped = p->terminal = true;
    km_event event = {.kind = KM_TERMINAL, .id = p->id, .outcome = KM_FAILED};
    km_diagnostic(&event, KM_HOSTF, "spawn failed");
    km_push(host, p, event);
}

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
}

static void km_reap(km_host *host, km_process *p) {
    if (p->reaped || p->terminal) return;
    if (host->force_waitpid_failure) {
        host->force_waitpid_failure = false;
        km_fail(host, p, "waitpid failed");
        return;
    }
    int status; pid_t result = waitpid(p->pid, &status, WNOHANG);
    if (result == 0) return;
    if (result < 0) { if (errno != EINTR) km_fail(host, p, "waitpid failed"); return; }
    p->reaped = true;
    if (WIFEXITED(status)) p->status = WEXITSTATUS(status); else if (WIFSIGNALED(status)) p->signal = WTERMSIG(status);
}

static void km_terminate(km_host *host, km_process *p, int outcome) {
    if (p->terminal || p->terminating) return;
    if (kill(-p->pid, SIGTERM) < 0 && errno != ESRCH) { km_fail(host, p, "SIGTERM failed"); return; }
    p->terminating = true; p->outcome = outcome; p->kill_deadline = km_now() + KM_GRACE_MS;
}

int km_host_cancel(km_host *host, int64_t id, bool timeout) {
    if (!host) return -1;
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) { km_terminate(host, &host->processes[i], timeout ? KM_TIMED_OUT : KM_CANCELLED); return 0; }
    return -1;
}

void km_host_cancel_all(km_host *host) {
    if (!host) return;
    for (int i = 0; i < host->len; i++) km_terminate(host, &host->processes[i], KM_CANCELLED);
}

int km_host_pump(km_host *host, int wait) {
    if (!host) return -1;
    int64_t now = km_now();
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (!p->terminal && p->deadline && now >= p->deadline) km_terminate(host, p, KM_TIMED_OUT);
        if (!p->terminal && p->terminating && !p->killed && now >= p->kill_deadline) { if (kill(-p->pid, SIGKILL) < 0 && errno != ESRCH) km_fail(host, p, "SIGKILL failed"); p->killed = true; }
    }
    int poll_wait = wait < 0 ? 0 : wait;
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        int64_t due = 0;
        if (!p->terminal && !p->terminating && p->deadline) due = p->deadline;
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
    for (int i = 0; i < host->len; i++) if (!host->processes[i].reaped) kill(-host->processes[i].pid, SIGTERM);
    poll(NULL, 0, KM_GRACE_MS);
    for (int i = 0; i < host->len; i++) {
        km_process *p = &host->processes[i];
        if (!p->reaped) { kill(-p->pid, SIGKILL); while (waitpid(p->pid, NULL, 0) < 0 && errno == EINTR) {} }
        km_release_process(p);
    }
    free(host->processes);
    while (host->first) { km_event_node *node = host->first; host->first = node->next; km_free_event(&node->event); free(node); }
    free(host);
}
