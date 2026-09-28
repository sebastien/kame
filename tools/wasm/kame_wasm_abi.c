#include "kame_wasm.h"
#include "wasm.h"

/* Clang may lower freestanding comparisons to these libc symbols even though
 * Solod maps source-level calls to builtins. Keep the implementations local to
 * the module instead of introducing a hosted C runtime. */
#undef memcmp
int memcmp(const void *left, const void *right, size_t size) {
  const uint8_t *a = left;
  const uint8_t *b = right;
  for (size_t i = 0; i < size; i++) {
    if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
  }
  return 0;
}

/*
 * This narrow C file is the deliberate bridge between JavaScript's primitive
 * wasm calling convention and Solod's generated slice-oriented functions. It
 * contains no engine policy and exposes no pointer-bearing C structure.
 */

extern unsigned char __heap_base;

static uint32_t kame_wasm_bump;
static char kame_wasm_diagnostic[256];
static uint32_t kame_wasm_diagnostic_len;

#define KAME_WASM_INSTANCE_CAPACITY 16u
#define KAME_WASM_SOURCE_CAPACITY 65536u
#define KAME_WASM_ARENA_CAPACITY 262144u
typedef struct {
  uint32_t generation;
  uint32_t source_len;
  bool live;
  bool retired;
  wasm_Runtime *runtime;
  host_Request pending;
  bool has_pending;
  bool event_pinned;
  char source[KAME_WASM_SOURCE_CAPACITY];
  char arena[KAME_WASM_ARENA_CAPACITY];
} kame_wasm_instance;
static kame_wasm_instance kame_wasm_instances[KAME_WASM_INSTANCE_CAPACITY];

static uint32_t kame_wasm_align(uint32_t value, uint32_t alignment) {
  return (value + alignment - 1u) & ~(alignment - 1u);
}

uint32_t kame_wasm_abi_version(void) { return KAME_WASM_EVENT_SCHEMA; }

uint32_t kame_wasm_alloc(uint32_t size, uint32_t alignment) {
  if (size == 0u) return 0u;
  if (alignment == 0u) alignment = 1u;
  if ((alignment & (alignment - 1u)) != 0u || alignment > 65536u) return 0u;
  if (kame_wasm_bump == 0u) kame_wasm_bump = (uint32_t)(uintptr_t)&__heap_base;
  uint32_t at = kame_wasm_align(kame_wasm_bump, alignment);
  if (at < kame_wasm_bump || size > UINT32_MAX - at) return 0u;
  uint32_t end = at + size;
  uint32_t bytes = __builtin_wasm_memory_size(0) * 65536u;
  if (end > bytes) {
    uint32_t needed = end - bytes;
    uint32_t pages = (needed + 65535u) / 65536u;
    if (__builtin_wasm_memory_grow(0, pages) == UINT32_MAX) return 0u;
  }
  kame_wasm_bump = end;
  return at;
}

uint32_t kame_wasm_free(uint32_t pointer, uint32_t size) {
  (void)pointer;
  (void)size;
  return KAME_WASM_OK;
}

static void kame_wasm_u16(uint8_t *dst, uint32_t at, uint16_t value) {
  dst[at] = (uint8_t)value;
  dst[at + 1u] = (uint8_t)(value >> 8u);
}

static void kame_wasm_u32(uint8_t *dst, uint32_t at, uint32_t value) {
  for (uint32_t i = 0; i < 4u; i++) dst[at + i] = (uint8_t)(value >> (i * 8u));
}

static void kame_wasm_u64(uint8_t *dst, uint32_t at, uint64_t value) {
  for (uint32_t i = 0; i < 8u; i++) dst[at + i] = (uint8_t)(value >> (i * 8u));
}

uint32_t kame_wasm_event_header(
    uint32_t dst, uint32_t dst_len, uint32_t kind,
    uint64_t root, uint64_t node, uint64_t request,
    int64_t generation, int64_t revision, uint32_t payload_len) {
  if (dst == 0u || dst_len < KAME_WASM_EVENT_HEADER_SIZE) {
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  uint8_t *out = (uint8_t *)(uintptr_t)dst;
  kame_wasm_u16(out, 0u, KAME_WASM_EVENT_SCHEMA);
  kame_wasm_u16(out, 2u, (uint16_t)kind);
  kame_wasm_u64(out, 4u, root);
  kame_wasm_u64(out, 12u, node);
  kame_wasm_u64(out, 20u, request);
  kame_wasm_u64(out, 28u, (uint64_t)generation);
  kame_wasm_u64(out, 36u, (uint64_t)revision);
  kame_wasm_u32(out, 44u, payload_len);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_copy(uint32_t dst, uint32_t dst_len,
                        uint32_t src, uint32_t src_len) {
  if (dst_len < src_len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (src_len == 0u) return KAME_WASM_OK;
  if (dst == 0u || src == 0u) return KAME_WASM_STATE_INVALID;
  uint8_t *out = (uint8_t *)(uintptr_t)dst;
  const uint8_t *in = (const uint8_t *)(uintptr_t)src;
  for (uint32_t i = 0; i < src_len; i++) out[i] = in[i];
  return KAME_WASM_OK;
}

static void kame_wasm_set_diagnostic(so_String code, so_String message) {
  kame_wasm_diagnostic_len = 0u;
  const so_String parts[] = {code, (so_String){": ", 2}, message};
  for (uint32_t p = 0; p < 3u; p++) {
    for (so_int i = 0; i < parts[p].len && kame_wasm_diagnostic_len < sizeof(kame_wasm_diagnostic); i++) {
      kame_wasm_diagnostic[kame_wasm_diagnostic_len++] = (char)parts[p].ptr[i];
    }
  }
}

static void kame_wasm_set_static_diagnostic(const char *code, const char *message) {
  kame_wasm_set_diagnostic(
      (so_String){code, (so_int)strlen(code)},
      (so_String){message, (so_int)strlen(message)});
}

static uint64_t kame_wasm_handle(uint32_t index, uint32_t generation) {
  return ((uint64_t)generation << 32u) | (uint64_t)(index + 1u);
}

static kame_wasm_instance *kame_wasm_instance_get(uint64_t handle) {
  uint32_t index = (uint32_t)handle;
  uint32_t generation = (uint32_t)(handle >> 32u);
  if (index == 0u || index > KAME_WASM_INSTANCE_CAPACITY || generation == 0u) return NULL;
  kame_wasm_instance *instance = &kame_wasm_instances[index - 1u];
  if (!instance->live || instance->generation != generation) return NULL;
  return instance;
}

static so_String kame_wasm_request_payload(host_Request request) {
  if (request.Kind == host_RequestProcess) return host_PayloadText(request.Payload, so_str("script"));
  return host_PayloadPath(request.Payload);
}

uint32_t kame_wasm_diagnostic_length(void) { return kame_wasm_diagnostic_len; }

uint32_t kame_wasm_diagnostic_copy(uint32_t dst, uint32_t dst_len) {
  if (dst_len < kame_wasm_diagnostic_len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (kame_wasm_diagnostic_len == 0u) return KAME_WASM_OK;
  if (dst == 0u) return KAME_WASM_STATE_INVALID;
  uint8_t *out = (uint8_t *)(uintptr_t)dst;
  for (uint32_t i = 0; i < kame_wasm_diagnostic_len; i++) out[i] = (uint8_t)kame_wasm_diagnostic[i];
  return KAME_WASM_OK;
}

uint64_t kame_wasm_instance_create(void) {
  for (uint32_t i = 0; i < KAME_WASM_INSTANCE_CAPACITY; i++) {
    kame_wasm_instance *instance = &kame_wasm_instances[i];
    if (instance->live || instance->retired) continue;
    if (instance->generation == 0u) instance->generation = 1u;
    instance->live = true;
    instance->source_len = 0u;
    return kame_wasm_handle(i, instance->generation);
  }
  kame_wasm_set_static_diagnostic("NO_MEMORY", "instance handle table is full");
  return 0u;
}

uint32_t kame_wasm_instance_free(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->has_pending) {
    host_Request_Free(&instance->pending, instance->runtime->Alloc);
    instance->pending = (host_Request){};
    instance->has_pending = false;
  }
  if (instance->runtime != NULL) {
    wasm_Runtime_Free(instance->runtime);
    instance->runtime = NULL;
  }
  instance->live = false;
  instance->source_len = 0u;
  instance->has_pending = false;
  instance->event_pinned = false;
  if (instance->generation == UINT32_MAX) instance->retired = true;
  else instance->generation++;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_source_compile(uint64_t handle, uint32_t source, uint32_t source_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if ((source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  if (instance->has_pending) {
    kame_wasm_set_static_diagnostic("PHASE_INVALID", "cannot compile while a host request is pending");
    return KAME_WASM_STATE_INVALID;
  }
  if (source_len > KAME_WASM_SOURCE_CAPACITY) {
    kame_wasm_set_static_diagnostic("NO_MEMORY", "source exceeds instance capacity");
    return KAME_WASM_NO_MEMORY;
  }
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_ValidateSource(mem_System, (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  if (result.Code.len != 0) {
    kame_wasm_set_diagnostic(result.Code, result.Message);
    wasm_PureResult_Free(&result, mem_System);
    so_heap_release(mark);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, mem_System);
  so_heap_release(mark);
  for (uint32_t i = 0; i < source_len; i++) instance->source[i] = ((const char *)(uintptr_t)source)[i];
  instance->source_len = source_len;
  if (instance->runtime != NULL) {
    wasm_Runtime_Free(instance->runtime);
    instance->runtime = NULL;
  }
  wasm_RuntimeStart started = wasm_NewRuntimeIn(
      (so_Slice){(so_byte *)instance->arena, (so_int)KAME_WASM_ARENA_CAPACITY, (so_int)KAME_WASM_ARENA_CAPACITY},
      (so_String){instance->source, (so_int)instance->source_len});
  if (started.Runtime == NULL) {
    kame_wasm_set_diagnostic(started.Result.Code, started.Result.Message);
    wasm_PureResult_Free(&started.Result, mem_System);
    return KAME_WASM_DIAGNOSTIC;
  }
  instance->runtime = started.Runtime;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_expression_begin(uint64_t handle, uint32_t source, uint32_t source_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  wasm_PureResult result = wasm_Runtime_RequestExpression(instance->runtime, (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  if (result.Code.len != 0) {
    kame_wasm_set_diagnostic(result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_expression_cancel(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || !wasm_Runtime_Cancel(instance->runtime)) return KAME_WASM_STATE_INVALID;
  if (instance->has_pending) {
    host_Request_Free(&instance->pending, instance->runtime->Alloc);
    instance->pending = (host_Request){};
    instance->has_pending = false;
  }
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_step(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  if (instance->has_pending) return 1u;
  host_NextResult next = wasm_Runtime_Step(instance->runtime);
  if (next.OK) {
    instance->pending = next.Request;
    instance->has_pending = true;
    instance->event_pinned = true;
    return 1u;
  }
  wasm_RuntimeResult result = wasm_Runtime_Result(instance->runtime);
  if (result.Done) {
    wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
    return 2u;
  }
  return 0u;
}

uint32_t kame_wasm_next_event_header(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned) return KAME_WASM_STATE_INVALID;
  so_String payload = kame_wasm_request_payload(instance->pending);
  return kame_wasm_event_header(dst, dst_len, 1u, handle, (uint64_t)instance->pending.NodeID,
      (uint64_t)instance->pending.ID, instance->pending.Generation, 0, (uint32_t)payload.len);
}

uint32_t kame_wasm_next_request_kind(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return 0u;
  if (!instance->has_pending || !instance->event_pinned) return 0u;
  return (uint32_t)instance->pending.Kind;
}

uint32_t kame_wasm_next_request_data_length(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || !instance->has_pending || !instance->event_pinned) return 0u;
  if (instance->pending.Kind != host_RequestWriteFile) return 0u;
  return (uint32_t)host_PayloadBytes(instance->pending.Payload, so_str("data")).len;
}

uint32_t kame_wasm_request_data_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned || instance->pending.Kind != host_RequestWriteFile) return KAME_WASM_STATE_INVALID;
  so_Slice data = host_PayloadBytes(instance->pending.Payload, so_str("data"));
  if (dst_len < (uint32_t)data.len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (data.len != 0 && dst == 0u) return KAME_WASM_STATE_INVALID;
  const uint8_t *bytes = (const uint8_t *)data.ptr;
  for (so_int i = 0; i < data.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = bytes[i];
  return KAME_WASM_OK;
}

uint32_t kame_wasm_event_payload_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned) return KAME_WASM_STATE_INVALID;
  so_String payload = kame_wasm_request_payload(instance->pending);
  if (dst_len < (uint32_t)payload.len) return KAME_WASM_BUFFER_TOO_SMALL;
  for (so_int i = 0; i < payload.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)payload.ptr[i];
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_event_discard(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned) return KAME_WASM_STATE_INVALID;
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_bytes(uint64_t handle, uint64_t request, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  /* A request that was already completed is a harmless late host callback.
   * There is no retained host buffer or runtime mutation to perform. */
  if (!instance->has_pending) return KAME_WASM_OK;
  if (instance->pending.ID != (int64_t)request) return KAME_WASM_STATE_INVALID;
  if (data_len != 0u && data == 0u) return KAME_WASM_STATE_INVALID;
  core_Value value = core_NewBytes(instance->runtime->Alloc, (so_Slice){(so_byte *)(uintptr_t)data, (so_int)data_len, (so_int)data_len});
  wasm_Runtime_Complete(instance->runtime, instance->pending, value, (diagnostic_Diagnostic){});
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_text(uint64_t handle, uint64_t request, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending) return KAME_WASM_OK;
  if (instance->pending.ID != (int64_t)request) return KAME_WASM_STATE_INVALID;
  if (data_len != 0u && data == 0u) return KAME_WASM_STATE_INVALID;
  core_Value value = core_NewString(instance->runtime->Alloc, (so_String){(const char *)(uintptr_t)data, (so_int)data_len});
  wasm_Runtime_Complete(instance->runtime, instance->pending, value, (diagnostic_Diagnostic){});
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_nil(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending) return KAME_WASM_OK;
  if (instance->pending.ID != (int64_t)request) return KAME_WASM_STATE_INVALID;
  wasm_Runtime_Complete(instance->runtime, instance->pending, (core_Value){}, (diagnostic_Diagnostic){});
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_failure(uint64_t handle, uint64_t request,
                                    uint32_t code, uint32_t code_len,
                                    uint32_t message, uint32_t message_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending) return KAME_WASM_OK;
  if (instance->pending.ID != (int64_t)request) return KAME_WASM_STATE_INVALID;
  if ((code_len != 0u && code == 0u) || (message_len != 0u && message == 0u)) return KAME_WASM_STATE_INVALID;
  diagnostic_Diagnostic diagnostic = (diagnostic_Diagnostic){
      .Code = (so_String){(const char *)(uintptr_t)code, (so_int)code_len},
      .Message = (so_String){(const char *)(uintptr_t)message, (so_int)message_len},
  };
  wasm_Runtime_Complete(instance->runtime, instance->pending, (core_Value){}, diagnostic);
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_result_copy(uint64_t handle, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  wasm_RuntimeResult result = wasm_Runtime_Result(instance->runtime);
  if (!result.Done) {
    wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  if (result.Diagnostic.Code.len != 0) {
    kame_wasm_set_diagnostic(result.Diagnostic.Code, result.Diagnostic.Message);
    wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  if (result.Value.Kind == core_Bytes) {
    uint32_t needed = (uint32_t)result.Value.Bytes.len;
    *(uint32_t *)(uintptr_t)out_len = needed;
    if (dst_len < needed) {
      wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
      return KAME_WASM_BUFFER_TOO_SMALL;
    }
    if (needed != 0u && dst == 0u) {
      wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
      return KAME_WASM_STATE_INVALID;
    }
    const uint8_t *bytes = (const uint8_t *)result.Value.Bytes.ptr;
    for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = bytes[i];
    wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_OK;
  }
  so_R_str_bool text = eval_Stringify(instance->runtime->Alloc, result.Value);
  wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
  if (!text.val2) {
    kame_wasm_set_static_diagnostic("EXPR_INVALID", "terminal value cannot be represented as text");
    return KAME_WASM_DIAGNOSTIC;
  }
  *(uint32_t *)(uintptr_t)out_len = (uint32_t)text.val.len;
  if (dst_len < (uint32_t)text.val.len) {
    mem_FreeString(instance->runtime->Alloc, text.val);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (text.val.len != 0 && dst == 0u) {
    mem_FreeString(instance->runtime->Alloc, text.val);
    return KAME_WASM_STATE_INVALID;
  }
  for (so_int i = 0; i < text.val.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)text.val.ptr[i];
  mem_FreeString(instance->runtime->Alloc, text.val);
  return KAME_WASM_OK;
}

static uint32_t kame_wasm_finish_pure(size_t mark, wasm_PureResult result,
                                      uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  if (result.Code.len != 0) {
    kame_wasm_set_diagnostic(result.Code, result.Message);
    wasm_PureResult_Free(&result, mem_System);
    so_heap_release(mark);
    return KAME_WASM_DIAGNOSTIC;
  }
  uint32_t needed = (uint32_t)result.Text.len;
  * (uint32_t *)(uintptr_t)out_len = needed;
  if (dst_len < needed) {
    wasm_PureResult_Free(&result, mem_System);
    so_heap_release(mark);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (needed != 0u && dst == 0u) {
    wasm_PureResult_Free(&result, mem_System);
    so_heap_release(mark);
    return KAME_WASM_STATE_INVALID;
  }
  for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = result.Text.ptr[i];
  wasm_PureResult_Free(&result, mem_System);
  so_heap_release(mark);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_eval_pure(uint32_t source, uint32_t source_len,
                             uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  if (out_len == 0u || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  kame_wasm_diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_EvaluatePure(mem_System, (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len);
}

uint32_t kame_wasm_eval_source_pure(uint32_t program, uint32_t program_len,
                                    uint32_t source, uint32_t source_len,
                                    uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  if (out_len == 0u || (program_len != 0u && program == 0u) || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  kame_wasm_diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_EvaluateSourcePure(
      mem_System,
      (so_String){(const char *)(uintptr_t)program, (so_int)program_len},
      (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len);
}

uint32_t kame_wasm_expression_request(uint64_t handle, uint32_t source,
                                      uint32_t source_len, uint32_t dst,
                                      uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (out_len == 0u || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  kame_wasm_diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_EvaluateSourcePure(
      mem_System,
      (so_String){instance->source, (so_int)instance->source_len},
      (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len);
}
