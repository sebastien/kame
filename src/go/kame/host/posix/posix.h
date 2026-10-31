#ifndef KAME_POSIX_H
#define KAME_POSIX_H

#include <stdbool.h>
#include <stdint.h>

// so_byte, so_Slice, so_String, and so_int are provided by the generated
// header prologue (so/builtin/builtin.h), which includes this file.

typedef struct km_host km_host;

typedef struct km_event {
    int kind;
    int64_t id;
    int64_t pid;
    int64_t pgid;
    int outcome;
    int status;
    int signal;
    so_byte *data;
    int dataLen;
    so_byte *diagnosticCode; // Stable code such as HOST_FAIL.
    int diagnosticCodeLen;
    so_byte *diagnostic;
    int diagnosticLen;
    so_byte *output;
    int stdoutLen;
    so_byte *errorOutput;
    int stderrLen;
    bool stdoutTruncated;
    bool stderrTruncated;
    int retainBytes;
    int *stageData;
    int stageCount;
    int64_t *metricData;
    int metricCount;
} km_event;

km_host *km_host_new(void);
// start/start_graph: 0 admitted, -1 failed with a queued terminal, -2 not
// admitted because the allocation-free rejected-event slot needs Next draining.
int km_host_start(km_host *, int64_t, so_Slice, so_Slice, so_String, so_Slice, int64_t, so_int, bool);
int km_host_start_graph(km_host *, int64_t, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, int64_t, so_int, so_String, so_String, bool, so_Slice, so_Slice, so_Slice);
typedef so_int (*km_stream_item)(void *, int64_t, so_int, so_Slice);
int km_host_start_topology(km_host *, int64_t, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, int64_t, so_int, so_String, so_String, bool, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, km_stream_item, void *, bool);
bool km_stream_operands_supported(void);
int km_event_stage_field(km_event *, int, int);
int64_t km_event_metric_field(km_event *, int, int);
int km_host_pump(km_host *, int);
bool km_host_next(km_host *, km_event *);
int km_host_cancel(km_host *, int64_t, bool);
int km_host_stop(km_host *, int64_t, int64_t);
void km_host_cancel_all(km_host *);
int km_host_active(km_host *);
// 0 acquired, -4 cancelled, -1 unavailable. Cancellation is not a cache miss.
int km_host_cache_lock(km_host *, so_String, int);
void km_host_cache_unlock(km_host *, int);
typedef struct km_lock_event {
    so_String State, Kind, Path;
    int64_t OwnerPID;
    so_String OwnerCommand;
    bool OwnerVerified;
    int64_t ElapsedMS, HeldMS;
} km_lock_event;
void km_cli_observe_locks(bool (*)(km_lock_event *), so_String);
int km_file_lock(int, so_String, so_String, bool);
void km_file_unlock(int);
void km_host_force_waitpid_failure(km_host *);
void km_event_force_allocation_failure(int);
void km_event_free(km_event *);
void km_host_free(km_host *);
int km_cli_install_signals(void);
int km_cli_take_signal(void);
bool km_cli_signal_pending(void);
// timeout_ns: 0 unbounded, negative already expired. Returns 0 success,
// 1 deadline exhausted, -1 I/O failure; written is valid on every exit.
int km_cli_write_output(int, so_Slice, int64_t, so_int *);
int km_cli_stderr_is_terminal(void);
int km_cli_stderr_width(void);
int km_cli_stdout_is_terminal(void);
// Interactive filter. 0 selected, 1 no match, 2 picker error, 127 not found,
// 130 cancelled, -1 host failure, -2 unsupported, -3 no terminal.
// Does not join a new process group: the child must stay in the foreground
// group so it can take the terminal. argv excludes the executable.
int km_pick_select(so_String executable, so_Slice argv, so_String input, so_Slice selection, so_int *selected);
int km_cli_stderr_height(void);
int km_cli_environment_size(void);
int km_cli_environment_copy(so_Slice);
bool km_cli_environment_names_case_insensitive(void);
int64_t km_file_modtime(so_String, bool);
bool km_file_watch_stamp(so_String, so_Slice);
int km_file_sync(void *);
int km_file_sync_parent(so_String);
int km_file_publish(so_String, so_String);
typedef struct km_watch km_watch;
km_watch *km_watch_new(void);
void km_watch_begin(km_watch *);
bool km_watch_add(km_watch *, so_String, bool);
bool km_watch_commit(km_watch *);
int km_watch_wait(km_watch *, int);
void km_watch_free(km_watch *);

#endif
