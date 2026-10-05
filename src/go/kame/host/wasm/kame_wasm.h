#ifndef KAME_WASM_H
#define KAME_WASM_H

#include <stdint.h>

/*
 * Freestanding ABI v1. All strings and byte payloads are pointer-plus-length
 * pairs in linear memory. No C structure containing a pointer crosses the ABI.
 * Symbol names remain implementation details until the generated Solod bridge
 * is linked; this header fixes only widths and envelope layout.
 */
#define KAME_WASM_EVENT_SCHEMA 1u
#define KAME_WASM_EVENT_HEADER_SIZE 48u

enum kame_wasm_status {
  KAME_WASM_OK = 0,
  KAME_WASM_HANDLE_INVALID = 1,
  KAME_WASM_NO_MEMORY = 2,
  KAME_WASM_BUFFER_TOO_SMALL = 3,
  KAME_WASM_STATE_INVALID = 4,
  KAME_WASM_DIAGNOSTIC = 5,
  KAME_WASM_HOST_NEEDED = 6
};

/* Ordered session descriptor; work kind: 1 implicit value, 2 selected value,
 * 3 process/rule (no implicit value display). Existing step/completion APIs
 * drive each work item on the same instance. */
uint32_t kame_wasm_session_compile(uint64_t handle, uint32_t data, uint32_t length);
uint32_t kame_wasm_register_plugins(uint64_t handle, uint32_t data, uint32_t length);
uint32_t kame_wasm_session_work_count(uint64_t handle);
uint32_t kame_wasm_session_work_kind(uint64_t handle, uint32_t index);
uint32_t kame_wasm_session_work_begin(uint64_t handle, uint32_t index);

/* Request kinds reported by kame_wasm_next_request_kind. Filesystem
 * operations that the engine carries as one read request are promoted to
 * distinct kinds so a host can dispatch without decoding payloads. */
enum kame_wasm_request_kind {
  KAME_WASM_REQUEST_READ_FILE = 1,
  KAME_WASM_REQUEST_WRITE_FILE = 2,
  KAME_WASM_REQUEST_PROCESS = 3,
  KAME_WASM_REQUEST_ENVIRONMENT = 4,
  KAME_WASM_REQUEST_STAT_PATH = 5,
  KAME_WASM_REQUEST_EXPAND_GLOB = 6,
  KAME_WASM_REQUEST_EXISTS = 7,
  KAME_WASM_REQUEST_WALL_TIME = 8,
  KAME_WASM_REQUEST_MONOTONIC_TIME = 9,
  KAME_WASM_REQUEST_CACHE_GET = 10,
  KAME_WASM_REQUEST_CACHE_PUT = 11,
  KAME_WASM_REQUEST_CACHE_DELETE = 12,
  // JSON argv array, executed directly; success is text, exits a status record.
  KAME_WASM_REQUEST_ARGV = 13,
  // JSON array of stage argv arrays; connects streams without a shell.
  KAME_WASM_REQUEST_PIPELINE = 14,
  // JSON {stages, input, output, append, setup}; per-stage cwd/env/timeout.
  KAME_WASM_REQUEST_REDIRECTED_GRAPH = 15,
  // JSON {script, outputs, environment?, shell?}; supplied environment is exact.
  // Target-bound collected shell calls declare an empty outputs array.
  KAME_WASM_REQUEST_RECIPE = 16,
  KAME_WASM_REQUEST_OUTPUT_EXISTS = 17,
  KAME_WASM_REQUEST_RESOLVE_TOOL = 18,
  KAME_WASM_REQUEST_TOOL_EXISTS = 19,
  // JSON canonical path array; completion is ordered decimal timestamp strings/null.
  KAME_WASM_REQUEST_FILE_TIMES = 20,
  // Recipe descriptor with outputs; prepares parents before structured execution.
  KAME_WASM_REQUEST_PREPARE_OUTPUTS = 21,
  KAME_WASM_REQUEST_TIMER = 22,
  KAME_WASM_REQUEST_PROCESS_CANCEL = 23,
  KAME_WASM_REQUEST_CACHE_LOCK = 24,
  KAME_WASM_REQUEST_CACHE_UNLOCK = 25,
  KAME_WASM_REQUEST_PLUGIN = 26
};

/* Terminal process outcomes reported by kame_wasm_process_terminal. */
enum kame_wasm_process_outcome {
  KAME_WASM_PROCESS_EXITED = 0,
  KAME_WASM_PROCESS_TIMED_OUT = 1,
  KAME_WASM_PROCESS_CANCELLED = 2,
  KAME_WASM_PROCESS_FAILED = 3
};

/* Module ABI discovery and linear-memory ownership. Allocation grows exported
 * memory only as needed, never beyond the linker-provided maximum. Returned
 * ranges belong to the caller until the owning instance is freed; free is a
 * deliberately no-op operation for the initial arena model. */
uint32_t kame_wasm_abi_version(void);
uint32_t kame_wasm_alloc(uint32_t size, uint32_t alignment);
uint32_t kame_wasm_free(uint32_t pointer, uint32_t size);

/* Encode the 48-byte event envelope at dst. Every 64-bit argument maps to a
 * JavaScript BigInt. The function returns a kame_wasm_status value. */
uint32_t kame_wasm_event_header(
    uint32_t dst, uint32_t dst_len, uint32_t kind,
    uint64_t root, uint64_t node, uint64_t request,
    int64_t generation, int64_t revision, uint32_t payload_len);

/* Copy exact byte ranges in linear memory. The source is never retained. */
uint32_t kame_wasm_copy(uint32_t dst, uint32_t dst_len,
                        uint32_t src, uint32_t src_len);

/* Evaluate one expression that needs no host capability. The required output
 * length is written to out_len; call once with dst_len zero to query it, then
 * again with caller-owned output memory. HOST_REQUIRED is reported through the
 * diagnostic API until request/completion exports are added. */
/* Parse a command line (NUL-separated args) with the shared CLI grammar and
 * copy the Invocation JSON. command is "" for the primary invocation. */
uint32_t kame_wasm_cli(uint64_t instance, uint32_t command, uint32_t command_len,
                       uint32_t args, uint32_t args_len,
                       uint32_t dst, uint32_t dst_len, uint32_t out_len);
uint32_t kame_wasm_eval_pure(uint32_t source, uint32_t source_len,
                             uint32_t dst, uint32_t dst_len, uint32_t out_len);
/* Parse lang ("expr"|"template"|"rule"|"script") and copy its schema-1 AST JSON
 * into caller-owned memory, mirroring the native `do parse` document. */
uint32_t kame_wasm_parse(uint64_t instance, uint32_t lang, uint32_t lang_len,
                         uint32_t name, uint32_t name_len,
                         uint32_t text, uint32_t text_len,
                         uint32_t dst, uint32_t dst_len, uint32_t out_len);
/* Format lang source into caller-owned memory. indent is "tabs" or "spaces";
 * width is the space count. A parse failure reports its diagnostic. */
uint32_t kame_wasm_format(uint64_t instance, uint32_t lang, uint32_t lang_len,
                          uint32_t name, uint32_t name_len,
                          uint32_t text, uint32_t text_len,
                          uint32_t indent, uint32_t indent_len, uint32_t width,
                          uint32_t dst, uint32_t dst_len, uint32_t out_len);
uint32_t kame_wasm_eval_source_pure(uint32_t program, uint32_t program_len,
                                    uint32_t source, uint32_t source_len,
                                    uint32_t dst, uint32_t dst_len, uint32_t out_len);
uint32_t kame_wasm_diagnostic_length(void);
uint32_t kame_wasm_diagnostic_copy(uint32_t dst, uint32_t dst_len);

/* Instance-scoped diagnostic retrieval. Each instance reserves a static
 * emergency slot at creation, so an out-of-memory condition reports NO_MEMORY
 * without allocating. Query the length, then copy into caller-owned memory. */
uint32_t kame_wasm_instance_diagnostic_length(uint64_t instance);
uint32_t kame_wasm_instance_diagnostic_copy(uint64_t instance, uint32_t dst,
                                            uint32_t dst_len);
/* Copy the current diagnostic's source span (two int32 slots). OK when a span
 * is present, STATE_INVALID otherwise. */
uint32_t kame_wasm_instance_diagnostic_span(uint64_t instance, uint32_t out_start,
                                            uint32_t out_end);

/* Instance handles are module-global generational handles. Source is copied
 * synchronously at compile time, so callers may reuse their input buffer on
 * return. */
uint64_t kame_wasm_instance_create(void);
uint32_t kame_wasm_instance_free(uint64_t instance);
/* Set the logical allocator budget before compilation: 1..16 MiB. The backing
 * arena remains 16 MiB. Exhausted instances allow diagnostics and destruction;
 * create a fresh instance to resume evaluation. */
uint32_t kame_wasm_instance_set_heap_limit(uint64_t instance, uint32_t limit);
/* Pure source-composition predicate query: JSON descriptor in, true/false out.
 * Uses the same length-query convention and emergency diagnostics as parse. */
uint32_t kame_wasm_declaration_predicate(uint64_t instance, uint32_t data,
    uint32_t data_len, uint32_t dst, uint32_t dst_len, uint32_t out_len);
uint32_t kame_wasm_source_compile(uint64_t instance, uint32_t source,
                                  uint32_t source_len);
/* Label the compiled source for diagnostics. Call before source_compile; an
 * unset or empty name keeps the "<wasm-source>" default. */
uint32_t kame_wasm_set_source_name(uint64_t instance, uint32_t name,
                                   uint32_t name_len);
uint32_t kame_wasm_expression_request(uint64_t instance, uint32_t source,
                                      uint32_t source_len, uint32_t dst,
                                      uint32_t dst_len, uint32_t out_len);

/* When enabled, target host requests are forwarded to the embedding host
 * through the step/event/completion loop instead of the in-memory filesystem.
 * Must be called before kame_wasm_target_begin. */
uint32_t kame_wasm_set_forwarding(uint64_t instance, uint32_t enabled);
/* Set the working directory the build runtime canonicalizes against. */
uint32_t kame_wasm_set_directory(uint64_t instance, uint32_t directory,
                                 uint32_t directory_len);

/* In-memory host population for target materialization. Both copy their input
 * synchronously and must be called before kame_wasm_target_begin. */
uint32_t kame_wasm_host_set_file(uint64_t instance, uint32_t path,
                                 uint32_t path_len, uint32_t data,
                                 uint32_t data_len);
uint32_t kame_wasm_host_set_env(uint64_t instance, uint32_t name,
                                uint32_t name_len, uint32_t value,
                                uint32_t value_len);
/* Schedule one target using the portable build runtime and its in-memory host. */
uint32_t kame_wasm_target_begin(uint64_t instance, uint32_t target,
                                uint32_t target_len);
/* Retain a shared build graph. begin takes a JSON array of target strings;
 * invalidate takes [{"kind":"file"|"glob","name":RESOURCE_KEY_NAME}].
 * state copies {busy, roots, resources}; repeated queries retain root interest.
 * Resources exclude produced outputs. The host owns polling and source reload. */
uint32_t kame_wasm_watch_begin(uint64_t instance, uint32_t data, uint32_t len);
uint32_t kame_wasm_watch_invalidate(uint64_t instance, uint32_t data, uint32_t len);
uint32_t kame_wasm_watch_state(uint64_t instance, uint32_t dst, uint32_t dst_len,
                               uint32_t out_len);
uint32_t kame_wasm_watch_cancel(uint64_t instance);
/* Compile the instance source for planning, then resolve one target plan into
 * caller-owned schema-1 JSON. expand resolves expression-form inputs. */
uint32_t kame_wasm_prepare(uint64_t instance);
uint32_t kame_wasm_plan(uint64_t instance, uint32_t target, uint32_t target_len,
                        uint32_t expand, uint32_t dst, uint32_t dst_len,
                        uint32_t out_len);
/* Walk a target graph. kind 0=inputs, 1=outputs, 2=span. depth -1 is unbounded;
 * expand (span only) resolves expression-form inputs. */
uint32_t kame_wasm_graph(uint64_t instance, uint32_t target, uint32_t target_len,
                         int32_t depth, uint32_t kind, uint32_t expand,
                         uint32_t dst, uint32_t dst_len, uint32_t out_len);
/* Copy the declared build tool names as a JSON array. */
uint32_t kame_wasm_tools(uint64_t instance, uint32_t dst, uint32_t dst_len,
                         uint32_t out_len);
/* Supply a host-resolved executable; an empty path marks an unavailable tool. */
uint32_t kame_wasm_set_tool_path(uint64_t instance, uint32_t name, uint32_t name_len,
                                uint32_t path, uint32_t path_len);
/* Check the selected dependency plan; output contains diagnostic JSON Lines.
 * HOST_NEEDED means step/service a forwarded request, then retry this query. */
uint32_t kame_wasm_tools_check(uint64_t instance, uint32_t target, uint32_t target_len,
                              uint32_t dst, uint32_t dst_len, uint32_t out_len);
/* Configure inspection grants before prepare; empty capability clears defaults. */
uint32_t kame_wasm_inspection_grant(uint64_t instance, uint32_t capability, uint32_t capability_len,
                                   uint32_t name, uint32_t name_len);
/* Pop one target lifecycle event as a schema-1 JSON line; a zero-length result
 * means no event is pending. */
uint32_t kame_wasm_target_event(uint64_t instance, uint32_t dst,
                                uint32_t dst_len, uint32_t out_len);
/* Streamed process progress for the currently pinned process request. The
 * terminal call reports the wait status (status and signal), the retained
 * stdout and stderr captures, and a kame_wasm_process_outcome. code and message
 * are empty on success, or a stable failure code with its message. */
uint32_t kame_wasm_process_started(uint64_t instance);
/* Detach a copied host request from the query pin while its host work runs.
 * Request-aware events and completions retain instance/generation ownership. */
uint32_t kame_wasm_request_detach(uint64_t instance, uint64_t request);
uint32_t kame_wasm_request_attach(uint64_t instance, uint64_t request);
uint32_t kame_wasm_process_started_request(uint64_t instance, uint64_t request);
uint32_t kame_wasm_process_exited_request(uint64_t instance, uint64_t request);
uint32_t kame_wasm_process_stream_request(uint64_t instance, uint64_t request,
                                         uint32_t stderr, uint32_t data, uint32_t data_len);
uint32_t kame_wasm_process_stream(uint64_t instance, uint32_t stderr,
                                  uint32_t data, uint32_t data_len);
uint32_t kame_wasm_process_terminal(uint64_t instance, int32_t status,
                                    int32_t signal, uint32_t outcome,
                                    uint32_t stdout, uint32_t stdout_len,
                                    uint32_t stderr, uint32_t stderr_len,
                                    uint32_t code, uint32_t code_len,
                                    uint32_t message, uint32_t message_len);

/* Asynchronous portable runner. Step returns 0 for progress/no event, 1 when
 * a host request is pinned, and 2 when the active expression is terminal. */
uint32_t kame_wasm_expression_begin(uint64_t instance, uint32_t source,
                                    uint32_t source_len);
uint32_t kame_wasm_expression_cancel(uint64_t instance);
/* Completed standalone-expression effects. kind is 1=out, 2=err, 3=yield,
 * or 0 when no effect remains. Copy advances to the next effect. */
uint32_t kame_wasm_expression_effect_kind(uint64_t instance);
uint32_t kame_wasm_expression_effect_length(uint64_t instance);
uint32_t kame_wasm_expression_effect_copy(uint64_t instance, uint32_t dst,
                                           uint32_t dst_len);
uint32_t kame_wasm_step(uint64_t instance);
uint32_t kame_wasm_next_event_header(uint64_t instance, uint32_t dst,
                                     uint32_t dst_len);
uint32_t kame_wasm_next_request_kind(uint64_t instance);
uint32_t kame_wasm_next_request_data_length(uint64_t instance);
uint32_t kame_wasm_request_data_copy(uint64_t instance, uint32_t dst,
                                     uint32_t dst_len);
/* Opaque cache request bytes. A cache get or delete carries a key; a cache put
 * carries a key and a record. Query the length, then copy before the pinned
 * event is discarded. */
uint32_t kame_wasm_next_request_key_length(uint64_t instance);
uint32_t kame_wasm_request_key_copy(uint64_t instance, uint32_t dst,
                                    uint32_t dst_len);
uint32_t kame_wasm_next_request_record_length(uint64_t instance);
uint32_t kame_wasm_request_record_copy(uint64_t instance, uint32_t dst,
                                       uint32_t dst_len);
uint32_t kame_wasm_event_payload_copy(uint64_t instance, uint32_t dst,
                                      uint32_t dst_len);
uint32_t kame_wasm_event_discard(uint64_t instance);
uint32_t kame_wasm_complete_bytes(uint64_t instance, uint64_t request,
                                  uint32_t data, uint32_t data_len);
uint32_t kame_wasm_complete_text(uint64_t instance, uint64_t request,
                                 uint32_t data, uint32_t data_len);
uint32_t kame_wasm_complete_nil(uint64_t instance, uint64_t request);
/* Completes the pinned request with a canonical JSON value. Structured results
 * such as stat records and glob lists cross the ABI as JSON; the engine owns
 * the parsed value and reports HOST_FAIL for malformed input. */
uint32_t kame_wasm_complete_json(uint64_t instance, uint64_t request,
                                 uint32_t data, uint32_t data_len);
/* Completes the pinned request with an owned runtime diagnostic. Code and
 * message are copied before this call returns. */
uint32_t kame_wasm_complete_failure(uint64_t instance, uint64_t request,
                                    uint32_t code, uint32_t code_len,
                                    uint32_t message, uint32_t message_len);
uint32_t kame_wasm_result_copy(uint64_t instance, uint32_t dst,
                               uint32_t dst_len, uint32_t out_len);
/* Shape of a completed target: 0 none, 1 definition value, 2 file path. */
uint32_t kame_wasm_result_kind(uint64_t instance);

/* Little-endian wire layout, encoded field-by-field rather than as this struct:
 * u16 schema, u16 kind, u64 root, u64 node, u64 request, i64 generation,
 * i64 revision, u32 payload_length.
 */

#endif
