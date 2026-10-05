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
} km_event;

km_host *km_host_new(void);
int km_host_start(km_host *, int64_t, so_Slice, so_Slice, so_String, so_Slice, int64_t, so_int, bool);
int km_host_start_graph(km_host *, int64_t, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, so_Slice, int64_t, so_int, so_String, so_String, bool);
int km_event_stage_field(km_event *, int, int);
int km_host_pump(km_host *, int);
bool km_host_next(km_host *, km_event *);
int km_host_cancel(km_host *, int64_t, bool);
int km_host_stop(km_host *, int64_t, int64_t);
void km_host_cancel_all(km_host *);
int km_host_active(km_host *);
int km_host_cache_lock(km_host *, so_String, int);
void km_host_cache_unlock(km_host *, int);
void km_host_force_waitpid_failure(km_host *);
void km_event_free(km_event *);
void km_host_free(km_host *);
int km_cli_install_signals(void);
int km_cli_take_signal(void);
int km_cli_stderr_is_terminal(void);
int km_cli_stderr_width(void);
int km_cli_environment_size(void);
int km_cli_environment_copy(so_Slice);
int64_t km_file_modtime(so_String, bool);

#endif
