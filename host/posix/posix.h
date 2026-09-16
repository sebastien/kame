#ifndef LITTLEMAKE_POSIX_H
#define LITTLEMAKE_POSIX_H

#include <stdbool.h>
#include <stdint.h>

// so_byte, so_Slice, so_String, and so_int are provided by the generated
// header prologue (so/builtin/builtin.h), which includes this file.

typedef struct lm_host lm_host;

typedef struct lm_event {
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
} lm_event;

lm_host *lm_host_new(void);
int lm_host_start(lm_host *, int64_t, so_Slice, so_Slice, so_String, so_Slice, int64_t, so_int);
int lm_host_pump(lm_host *, int);
bool lm_host_next(lm_host *, lm_event *);
int lm_host_cancel(lm_host *, int64_t, bool);
void lm_host_cancel_all(lm_host *);
int lm_host_active(lm_host *);
void lm_host_force_waitpid_failure(lm_host *);
void lm_event_free(lm_event *);
void lm_host_free(lm_host *);

#endif
