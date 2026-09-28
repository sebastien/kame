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
  KAME_WASM_DIAGNOSTIC = 5
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
uint32_t kame_wasm_eval_pure(uint32_t source, uint32_t source_len,
                             uint32_t dst, uint32_t dst_len, uint32_t out_len);
uint32_t kame_wasm_eval_source_pure(uint32_t program, uint32_t program_len,
                                    uint32_t source, uint32_t source_len,
                                    uint32_t dst, uint32_t dst_len, uint32_t out_len);
uint32_t kame_wasm_diagnostic_length(void);
uint32_t kame_wasm_diagnostic_copy(uint32_t dst, uint32_t dst_len);

/* Instance handles are module-global generational handles. Source is copied
 * synchronously at compile time, so callers may reuse their input buffer on
 * return. */
uint64_t kame_wasm_instance_create(void);
uint32_t kame_wasm_instance_free(uint64_t instance);
uint32_t kame_wasm_source_compile(uint64_t instance, uint32_t source,
                                  uint32_t source_len);
uint32_t kame_wasm_expression_request(uint64_t instance, uint32_t source,
                                      uint32_t source_len, uint32_t dst,
                                      uint32_t dst_len, uint32_t out_len);

/* Asynchronous portable runner. Step returns 0 for progress/no event, 1 when
 * a host request is pinned, and 2 when the active expression is terminal. */
uint32_t kame_wasm_expression_begin(uint64_t instance, uint32_t source,
                                    uint32_t source_len);
uint32_t kame_wasm_expression_cancel(uint64_t instance);
uint32_t kame_wasm_step(uint64_t instance);
uint32_t kame_wasm_next_event_header(uint64_t instance, uint32_t dst,
                                     uint32_t dst_len);
uint32_t kame_wasm_next_request_kind(uint64_t instance);
uint32_t kame_wasm_next_request_data_length(uint64_t instance);
uint32_t kame_wasm_request_data_copy(uint64_t instance, uint32_t dst,
                                     uint32_t dst_len);
uint32_t kame_wasm_event_payload_copy(uint64_t instance, uint32_t dst,
                                      uint32_t dst_len);
uint32_t kame_wasm_event_discard(uint64_t instance);
uint32_t kame_wasm_complete_bytes(uint64_t instance, uint64_t request,
                                  uint32_t data, uint32_t data_len);
uint32_t kame_wasm_complete_text(uint64_t instance, uint64_t request,
                                 uint32_t data, uint32_t data_len);
uint32_t kame_wasm_complete_nil(uint64_t instance, uint64_t request);
/* Completes the pinned request with an owned runtime diagnostic. Code and
 * message are copied before this call returns. */
uint32_t kame_wasm_complete_failure(uint64_t instance, uint64_t request,
                                    uint32_t code, uint32_t code_len,
                                    uint32_t message, uint32_t message_len);
uint32_t kame_wasm_result_copy(uint64_t instance, uint32_t dst,
                               uint32_t dst_len, uint32_t out_len);

/* Little-endian wire layout, encoded field-by-field rather than as this struct:
 * u16 schema, u16 kind, u64 root, u64 node, u64 request, i64 generation,
 * i64 revision, u32 payload_length.
 */

#endif
