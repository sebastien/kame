//go:build ignore
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

// Event kind and outcome values must match posix.EventKind and posix.Outcome.
enum { LM_STARTED, LM_STDOUT, LM_STDERR, LM_TERMINAL };
enum { LM_EXITED, LM_TIMED_OUT, LM_CANCELLED, LM_FAILED };
enum { LM_QUEUE_LIMIT = 256 * 1024, LM_CHUNK = 16 * 1024, LM_GRACE_MS = 100 };

// Host diagnostics use stable codes from docs/spec/011-diagnostics.md.
#define LM_HOSTF "LM-HOSTF"

typedef struct lm_process {
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
} lm_process;

typedef struct lm_event_node {
    lm_event event;
    int64_t process_id;
    struct lm_event_node *next;
} lm_event_node;

struct lm_host {
    lm_process *processes;
    int len, cap;
    lm_event_node *first, *last;
    bool force_waitpid_failure;
};

static int64_t lm_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static void lm_free_event(lm_event *event) {
    free(event->data); free(event->diagnosticCode); free(event->diagnostic);
    free(event->output); free(event->errorOutput);
    memset(event, 0, sizeof(*event));
}

void lm_event_free(lm_event *event) { lm_free_event(event); }

static bool lm_push(lm_host *host, lm_process *process, lm_event event) {
    lm_event_node *node = calloc(1, sizeof(*node));
    if (!node) { lm_free_event(&event); return false; }
    node->event = event; node->process_id = process ? process->id : 0;
    if (host->last) host->last->next = node; else host->first = node;
    host->last = node;
    return true;
}

static so_byte *lm_copy(const void *data, int len) {
    if (len <= 0) return NULL;
    so_byte *out = malloc((size_t)len);
    if (out) memcpy(out, data, (size_t)len);
    return out;
}

static char *lm_cstring(so_String value) {
    if (value.len < 0 || (value.len && (!value.ptr || memchr(value.ptr, '\0', (size_t)value.len)))) return NULL;
    char *out = calloc((size_t)value.len + 1, 1);
    if (out && value.len) memcpy(out, value.ptr, (size_t)value.len);
    return out;
}

static void lm_free_strings(char **strings, int count) {
    if (!strings) return;
    for (int i = 0; i < count; i++) free(strings[i]);
    free(strings);
}

static void lm_diagnostic(lm_event *event, const char *code, const char *text) {
    int cn = (int)strlen(code);
    event->diagnosticCode = lm_copy(code, cn); event->diagnosticCodeLen = cn;
    int n = (int)strlen(text);
    event->diagnostic = lm_copy(text, n); event->diagnosticLen = n;
}

static void lm_close(int *fd) { if (*fd >= 0) { close(*fd); *fd = -1; } }

static bool lm_close_checked(int *fd) {
    if (*fd < 0) return true;
    int saved = close(*fd); *fd = -1;
    return saved == 0;
}

static void lm_release_process(lm_process *p) {
    lm_close(&p->outfd); lm_close(&p->errfd); lm_close(&p->execfd);
    free(p->stdout_data); free(p->stderr_data);
    memset(p, 0, sizeof(*p)); p->outfd = p->errfd = p->execfd = -1;
}

static bool lm_append(so_byte **data, int *len, int *cap, const so_byte *chunk, int n, int limit, bool *truncated) {
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

static void lm_emit_terminal(lm_host *host, lm_process *p, int outcome, const char *diagnostic) {
    if (p->terminal) return;
    p->terminal = true;
    lm_event event = { .kind = LM_TERMINAL, .id = p->id, .pid = p->pid, .pgid = p->pid, .outcome = outcome, .status = p->status, .signal = p->signal, .output = lm_copy(p->stdout_data, p->stdout_len), .stdoutLen = p->stdout_len, .errorOutput = lm_copy(p->stderr_data, p->stderr_len), .stderrLen = p->stderr_len, .stdoutTruncated = p->stdout_truncated, .stderrTruncated = p->stderr_truncated };
    if (diagnostic) lm_diagnostic(&event, LM_HOSTF, diagnostic);
    lm_push(host, p, event);
}

static void lm_fail(lm_host *host, lm_process *p, const char *diagnostic) {
    if (!p->reaped) kill(-p->pid, SIGKILL);
    lm_close(&p->outfd); lm_close(&p->errfd);
    lm_emit_terminal(host, p, LM_FAILED, diagnostic);
}

static lm_process *lm_process_for(lm_host *host, int64_t id) {
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return &host->processes[i];
    return NULL;
}

static void lm_remove_process(lm_host *host, int64_t id) {
    for (int i = 0; i < host->len; i++) {
        if (host->processes[i].id != id) continue;
        lm_release_process(&host->processes[i]);
        host->len--;
        if (i != host->len) host->processes[i] = host->processes[host->len];
        return;
    }
}

static int lm_set_nonblock(int fd) {
    int flags = fcntl(fd, F_GETFL);
    return flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) < 0 ? -1 : 0;
}

lm_host *lm_host_new(void) { return calloc(1, sizeof(lm_host)); }

static int lm_spawn_failed(lm_host *host, int64_t id, const char *message) {
    lm_event event = {.kind = LM_TERMINAL, .id = id, .outcome = LM_FAILED};
    lm_diagnostic(&event, LM_HOSTF, message);
    lm_push(host, NULL, event);
    return -1;
}

int lm_host_start(lm_host *host, int64_t id, so_Slice shell, so_Slice script, so_String directory, so_Slice environment, int64_t timeout, so_int retain) {
    if (!host) return -1;
    // Every rejected request emits exactly one failed terminal event.
    if (id == 0 || shell.len == 0 || timeout < 0 || retain < 0) return lm_spawn_failed(host, id, "invalid request");
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) return lm_spawn_failed(host, id, "request ID already active");
    int out[2] = {-1, -1}, err[2] = {-1, -1}, execerr[2] = {-1, -1};
    if (pipe(out) || pipe(err) || pipe(execerr)) goto fail;
    if (fcntl(execerr[1], F_SETFD, FD_CLOEXEC) < 0) goto fail;
    char **argv = calloc((size_t)shell.len + 2, sizeof(char *));
    char **envp = calloc((size_t)environment.len + 1, sizeof(char *));
    char *cwd = lm_cstring(directory);
    if (!argv || !envp || !cwd || (script.len && (!script.ptr || memchr(script.ptr, '\0', (size_t)script.len)))) { lm_free_strings(argv, (int)shell.len + 1); lm_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    so_String *shells = shell.ptr, *envs = environment.ptr;
    for (int i = 0; i < shell.len; i++) {
        argv[i] = lm_cstring(shells[i]);
        if (!argv[i]) { lm_free_strings(argv, (int)shell.len + 1); lm_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    }
    argv[shell.len] = calloc((size_t)script.len + 1, 1);
    if (!argv[shell.len]) { lm_free_strings(argv, (int)shell.len + 1); lm_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    memcpy(argv[shell.len], script.ptr, (size_t)script.len);
    for (int i = 0; i < environment.len; i++) {
        envp[i] = lm_cstring(envs[i]);
        if (!envp[i]) { lm_free_strings(argv, (int)shell.len + 1); lm_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    }
    pid_t pid = fork();
    if (pid < 0) { lm_free_strings(argv, (int)shell.len + 1); lm_free_strings(envp, (int)environment.len); free(cwd); goto fail; }
    if (pid == 0) {
        int child_errno;
        close(out[0]); close(err[0]); close(execerr[0]);
        if (setpgid(0, 0) || dup2(out[1], STDOUT_FILENO) < 0 || dup2(err[1], STDERR_FILENO) < 0 || chdir(cwd) < 0) {
            child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127);
        }
        close(out[1]); close(err[1]); execve(argv[0], argv, envp);
        child_errno = errno; write(execerr[1], &child_errno, sizeof(child_errno)); _exit(127);
    }
    lm_free_strings(argv, (int)shell.len + 1); lm_free_strings(envp, (int)environment.len); free(cwd);
    close(out[1]); close(err[1]); close(execerr[1]); out[1] = err[1] = execerr[1] = -1;
    setpgid(pid, pid);
    if (lm_set_nonblock(out[0]) || lm_set_nonblock(err[0]) || lm_set_nonblock(execerr[0])) { kill(-pid, SIGKILL); waitpid(pid, NULL, 0); lm_close(&out[0]); lm_close(&err[0]); lm_close(&execerr[0]); return lm_spawn_failed(host, id, "spawn failed"); }
    if (host->len == host->cap) {
        int cap = host->cap ? host->cap * 2 : 4;
        lm_process *processes = realloc(host->processes, (size_t)cap * sizeof(*processes));
        if (!processes) { kill(-pid, SIGKILL); waitpid(pid, NULL, 0); lm_close(&out[0]); lm_close(&err[0]); lm_close(&execerr[0]); return lm_spawn_failed(host, id, "spawn failed"); }
        host->processes = processes; host->cap = cap;
    }
    lm_process *p = &host->processes[host->len++];
    memset(p, 0, sizeof(*p)); p->id = id; p->pid = pid; p->outfd = out[0]; p->errfd = err[0]; p->execfd = execerr[0]; p->retain = retain; p->deadline = timeout ? lm_now() + timeout : 0;
    return 0;
fail:
    lm_close(&out[0]); lm_close(&out[1]); lm_close(&err[0]); lm_close(&err[1]); lm_close(&execerr[0]); lm_close(&execerr[1]);
    return lm_spawn_failed(host, id, "spawn failed");
}

static void lm_exec_ready(lm_host *host, lm_process *p) {
    int child_errno;
    ssize_t n = read(p->execfd, &child_errno, sizeof(child_errno));
    if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)) return;
    if (n == 0) {
        if (!lm_close_checked(&p->execfd)) { lm_fail(host, p, "exec pipe close failed"); return; }
        p->started = true;
        lm_push(host, p, (lm_event){.kind = LM_STARTED, .id = p->id, .pid = p->pid, .pgid = p->pid});
        return;
    }
    lm_close(&p->execfd); lm_close(&p->outfd); lm_close(&p->errfd);
    pid_t result;
    do { result = waitpid(p->pid, NULL, 0); } while (result < 0 && errno == EINTR);
    if (result != p->pid) { lm_fail(host, p, "waitpid failed"); return; }
    p->reaped = p->terminal = true;
    lm_event event = {.kind = LM_TERMINAL, .id = p->id, .outcome = LM_FAILED};
    lm_diagnostic(&event, LM_HOSTF, "spawn failed");
    lm_push(host, p, event);
}

static void lm_read(lm_host *host, lm_process *p, int *fd, int kind) {
    if (*fd < 0 || p->queued >= LM_QUEUE_LIMIT) return;
    int want = LM_QUEUE_LIMIT - p->queued; if (want > LM_CHUNK) want = LM_CHUNK;
    so_byte buffer[LM_CHUNK]; ssize_t n = read(*fd, buffer, (size_t)want);
    if (n == 0) { if (!lm_close_checked(fd)) lm_fail(host, p, "pipe close failed"); return; }
    if (n < 0) { if (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR) lm_fail(host, p, "pipe read failed"); return; }
    lm_event event = {.kind = kind, .id = p->id, .pid = p->pid, .pgid = p->pid, .data = lm_copy(buffer, (int)n), .dataLen = (int)n};
    if (!event.data || !lm_push(host, p, event)) { lm_fail(host, p, "output queue allocation failed"); return; }
    p->queued += (int)n;
    if (kind == LM_STDOUT) { if (!lm_append(&p->stdout_data, &p->stdout_len, &p->stdout_cap, buffer, (int)n, p->retain, &p->stdout_truncated)) lm_fail(host, p, "retained output allocation failed"); }
    else if (!lm_append(&p->stderr_data, &p->stderr_len, &p->stderr_cap, buffer, (int)n, p->retain, &p->stderr_truncated)) lm_fail(host, p, "retained output allocation failed");
}

static void lm_reap(lm_host *host, lm_process *p) {
    if (p->reaped || p->terminal) return;
    if (host->force_waitpid_failure) {
        host->force_waitpid_failure = false;
        lm_fail(host, p, "waitpid failed");
        return;
    }
    int status; pid_t result = waitpid(p->pid, &status, WNOHANG);
    if (result == 0) return;
    if (result < 0) { if (errno != EINTR) lm_fail(host, p, "waitpid failed"); return; }
    p->reaped = true;
    if (WIFEXITED(status)) p->status = WEXITSTATUS(status); else if (WIFSIGNALED(status)) p->signal = WTERMSIG(status);
}

static void lm_terminate(lm_host *host, lm_process *p, int outcome) {
    if (p->terminal || p->terminating) return;
    if (kill(-p->pid, SIGTERM) < 0 && errno != ESRCH) { lm_fail(host, p, "SIGTERM failed"); return; }
    p->terminating = true; p->outcome = outcome; p->kill_deadline = lm_now() + LM_GRACE_MS;
}

int lm_host_cancel(lm_host *host, int64_t id, bool timeout) {
    if (!host) return -1;
    for (int i = 0; i < host->len; i++) if (host->processes[i].id == id) { lm_terminate(host, &host->processes[i], timeout ? LM_TIMED_OUT : LM_CANCELLED); return 0; }
    return -1;
}

void lm_host_cancel_all(lm_host *host) {
    if (!host) return;
    for (int i = 0; i < host->len; i++) lm_terminate(host, &host->processes[i], LM_CANCELLED);
}

int lm_host_pump(lm_host *host, int wait) {
    if (!host) return -1;
    int64_t now = lm_now();
    for (int i = 0; i < host->len; i++) {
        lm_process *p = &host->processes[i];
        if (!p->terminal && p->deadline && now >= p->deadline) lm_terminate(host, p, LM_TIMED_OUT);
        if (!p->terminal && p->terminating && !p->killed && now >= p->kill_deadline) { if (kill(-p->pid, SIGKILL) < 0 && errno != ESRCH) lm_fail(host, p, "SIGKILL failed"); p->killed = true; }
    }
    int poll_wait = wait < 0 ? 0 : wait;
    for (int i = 0; i < host->len; i++) {
        lm_process *p = &host->processes[i];
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
    lm_process **owners = calloc((size_t)host->len * 3, sizeof(*owners));
    int *kinds = calloc((size_t)host->len * 3, sizeof(*kinds)); int count = 0;
    if ((!fds || !owners || !kinds) && host->len) {
        free(fds); free(owners); free(kinds);
        for (int i = 0; i < host->len; i++) lm_fail(host, &host->processes[i], "poll allocation failed");
        return -1;
    }
    for (int i = 0; i < host->len; i++) {
        lm_process *p = &host->processes[i];
        if (!p->terminal && p->execfd >= 0) { fds[count].fd = p->execfd; fds[count].events = POLLIN|POLLHUP; owners[count] = p; kinds[count++] = 99; }
        if (!p->terminal && p->started && p->queued < LM_QUEUE_LIMIT && p->outfd >= 0) { fds[count].fd = p->outfd; fds[count].events = POLLIN|POLLHUP; owners[count] = p; kinds[count++] = LM_STDOUT; }
        if (!p->terminal && p->started && p->queued < LM_QUEUE_LIMIT && p->errfd >= 0) { fds[count].fd = p->errfd; fds[count].events = POLLIN|POLLHUP; owners[count] = p; kinds[count++] = LM_STDERR; }
    }
    int ready; do { ready = poll(fds, (nfds_t)count, poll_wait); } while (ready < 0 && errno == EINTR);
    if (ready < 0) {
        free(fds); free(owners); free(kinds);
        for (int i = 0; i < host->len; i++) lm_fail(host, &host->processes[i], "poll failed");
        return -1;
    }
    for (int i = 0; i < count; i++) if (fds[i].revents) {
        if (kinds[i] == 99) lm_exec_ready(host, owners[i]);
        else lm_read(host, owners[i], kinds[i] == LM_STDOUT ? &owners[i]->outfd : &owners[i]->errfd, kinds[i]);
    }
    free(fds); free(owners); free(kinds);
    for (int i = 0; i < host->len; i++) { lm_process *p = &host->processes[i]; lm_reap(host, p); if (!p->terminal && p->reaped && p->outfd < 0 && p->errfd < 0) lm_emit_terminal(host, p, p->terminating ? p->outcome : LM_EXITED, NULL); }
    return 0;
}

bool lm_host_next(lm_host *host, lm_event *event) {
    if (!host || !host->first) return false;
    lm_event_node *node = host->first; host->first = node->next; if (!host->first) host->last = NULL;
    *event = node->event;
    lm_process *process = node->process_id ? lm_process_for(host, node->process_id) : NULL;
    if (process && (event->kind == LM_STDOUT || event->kind == LM_STDERR)) process->queued -= event->dataLen;
    if (event->kind == LM_TERMINAL) lm_remove_process(host, node->process_id);
    free(node); return true;
}

int lm_host_active(lm_host *host) {
    int active = 0;
    if (!host) return 0;
    for (int i = 0; i < host->len; i++) if (host->processes[i].started && !host->processes[i].terminal) active++;
    return active;
}

void lm_host_force_waitpid_failure(lm_host *host) {
    if (host) host->force_waitpid_failure = true;
}

void lm_host_free(lm_host *host) {
    if (!host) return;
    for (int i = 0; i < host->len; i++) if (!host->processes[i].reaped) kill(-host->processes[i].pid, SIGTERM);
    poll(NULL, 0, LM_GRACE_MS);
    for (int i = 0; i < host->len; i++) {
        lm_process *p = &host->processes[i];
        if (!p->reaped) { kill(-p->pid, SIGKILL); while (waitpid(p->pid, NULL, 0) < 0 && errno == EINTR) {} }
        lm_release_process(p);
    }
    free(host->processes);
    while (host->first) { lm_event_node *node = host->first; host->first = node->next; lm_free_event(&node->event); free(node); }
    free(host);
}
