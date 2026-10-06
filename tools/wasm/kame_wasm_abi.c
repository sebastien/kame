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
#define KAME_WASM_ARENA_CAPACITY (16u * 1024u * 1024u)
typedef struct kame_wasm_parked_request {
  host_Request pending;
  uint64_t request;
  struct kame_wasm_parked_request *next;
} kame_wasm_parked_request;
typedef struct {
  uint64_t owner;        /* handle-table owner token for this instance */
  uint32_t source_len;
  bool live;
  bool exhausted;
  uint32_t heap_limit;
  wasm_Heap heap;
  wasm_Runtime *runtime;
  host_Request pending;
  kame_wasm_parked_request *parked;
  bool has_pending;
  bool event_pinned;
  uint64_t request;      /* handle of the pinned request, zero when none */
  uint64_t last_request; /* most recent request handle, for late completions */
  uint64_t root;         /* handle of the active expression root */
  uint64_t node;         /* handle of the active expression node */
  uint32_t diagnostic_len; /* per-instance emergency diagnostic length */
  char diagnostic[256];    /* static, allocation-free diagnostic slot */
  bool diagnostic_has_span; /* whether the diagnostic carries a source span */
  int32_t diagnostic_span_start;
  int32_t diagnostic_span_end;
  uint32_t source_name_len; /* label reported in diagnostics for the source */
  char source_name[256];
  char source[KAME_WASM_SOURCE_CAPACITY];
  /* Lazily allocate and reuse each slot's arena: captures need more than the
   * old 256 KiB, but reserving all 16 arenas exceeds module memory. */
  char *arena;
} kame_wasm_instance;
static kame_wasm_instance kame_wasm_instances[KAME_WASM_INSTANCE_CAPACITY];

extern int setjmp(void *environment) __attribute__((returns_twice));
extern void longjmp(void *environment, int value) __attribute__((noreturn));
typedef struct kame_wasm_checkpoint {
  void *environment[4];
  struct kame_wasm_checkpoint *previous;
  kame_wasm_instance *instance;
  size_t system_mark;
  bool restore_system;
} kame_wasm_checkpoint;
static kame_wasm_checkpoint *kame_wasm_active_checkpoint;
static void kame_wasm_checkpoint_end(kame_wasm_checkpoint *checkpoint) {
  kame_wasm_active_checkpoint = checkpoint->previous;
}
static bool kame_wasm_exhausted(kame_wasm_instance *instance) {
  return instance != NULL && instance->exhausted;
}
#define KAME_WASM_CHECKPOINT(instance_, failure_, temporary_) \
  kame_wasm_checkpoint checkpoint __attribute__((cleanup(kame_wasm_checkpoint_end))) = { \
    .previous = kame_wasm_active_checkpoint, .instance = (instance_), \
    .system_mark = so_heap_mark(), .restore_system = (temporary_) }; \
  kame_wasm_active_checkpoint = &checkpoint; \
  if (kame_wasm_exhausted(instance_) || setjmp(checkpoint.environment)) return (failure_)


/*
 * One module-global handle table owns every ABI handle: instances plus their
 * root, node, and request handles. Its entries record the owning instance token
 * so a handle from another instance is rejected. The table lives outside any
 * instance arena, so its lifetime is the module's, not a single instance's.
 */
static wasm_Table *kame_wasm_handles;
static uint64_t kame_wasm_next_owner;

static wasm_Table *kame_wasm_table(void) {
  if (kame_wasm_handles == NULL) kame_wasm_handles = wasm_NewTable(mem_System);
  return kame_wasm_handles;
}

static uint64_t kame_wasm_next_token(void) {
  kame_wasm_next_owner++;
  if (kame_wasm_next_owner == 0u) kame_wasm_next_owner = 1u;
  return kame_wasm_next_owner;
}

static uint64_t kame_wasm_child_handle(kame_wasm_instance *instance, uint64_t value) {
  return (uint64_t)wasm_Table_Add(kame_wasm_table(), instance->owner, value);
}

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

static void kame_wasm_write_diagnostic(char *dst, uint32_t cap, uint32_t *out_len, so_String code, so_String message) {
  *out_len = 0u;
  const so_String parts[] = {code, (so_String){": ", 2}, message};
  for (uint32_t p = 0; p < 3u; p++) {
    for (so_int i = 0; i < parts[p].len && *out_len < cap; i++) {
      dst[(*out_len)++] = (char)parts[p].ptr[i];
    }
  }
}

/* Module-global diagnostic, used by host-free entry points and by failures that
 * happen before an instance exists. Instance entry points write to the
 * instance's static emergency slot instead. */
static void kame_wasm_set_diagnostic(so_String code, so_String message) {
  kame_wasm_write_diagnostic(kame_wasm_diagnostic, (uint32_t)sizeof(kame_wasm_diagnostic), &kame_wasm_diagnostic_len, code, message);
}

static void kame_wasm_set_static_diagnostic(const char *code, const char *message) {
  kame_wasm_set_diagnostic(
      (so_String){code, (so_int)strlen(code)},
      (so_String){message, (so_int)strlen(message)});
}

static void kame_wasm_instance_set_diagnostic(kame_wasm_instance *instance, so_String code, so_String message) {
  instance->diagnostic_has_span = false;
  instance->diagnostic_span_start = 0;
  instance->diagnostic_span_end = 0;
  kame_wasm_write_diagnostic(instance->diagnostic, (uint32_t)sizeof(instance->diagnostic), &instance->diagnostic_len, code, message);
}

static void kame_wasm_instance_set_diagnostic_span(kame_wasm_instance *instance, so_String code, so_String message, int32_t start, int32_t end) {
  kame_wasm_instance_set_diagnostic(instance, code, message);
  instance->diagnostic_has_span = true;
  instance->diagnostic_span_start = start;
  instance->diagnostic_span_end = end;
}

static void kame_wasm_instance_set_static_diagnostic(kame_wasm_instance *instance, const char *code, const char *message) {
  kame_wasm_instance_set_diagnostic(
      instance,
      (so_String){code, (so_int)strlen(code)},
      (so_String){message, (so_int)strlen(message)});
}

static bool kame_wasm_oom_message(const char *message) {
  const char expected[] = "out of memory";
  for (size_t i = 0; i < sizeof(expected); i++) {
    if (message[i] != expected[i]) return false;
  }
  return true;
}

_Noreturn void kame_wasm_panic(const char *message) {
  kame_wasm_checkpoint *checkpoint = kame_wasm_active_checkpoint;
  if (checkpoint != NULL && kame_wasm_oom_message(message)) {
    kame_wasm_instance *instance = checkpoint->instance;
    if (instance != NULL) {
      instance->exhausted = true;
      instance->runtime = NULL;
      instance->pending = (host_Request){};
      instance->parked = NULL;
      instance->has_pending = false;
      instance->event_pinned = false;
      instance->request = instance->root = instance->node = 0u;
      wasm_Heap_Reset(&instance->heap);
      kame_wasm_instance_set_static_diagnostic(instance, "NO_MEMORY", "instance heap exhausted");
    } else {
      kame_wasm_set_static_diagnostic("NO_MEMORY", "module heap exhausted");
    }
    if (checkpoint->restore_system) so_heap_release(checkpoint->system_mark);
    longjmp(checkpoint->environment, 1);
  }
  /* Assertions and invalid memory accesses remain programmer-error traps. */
  __builtin_trap();
}


static kame_wasm_instance *kame_wasm_instance_get(uint64_t handle) {
  if (handle == 0u) return NULL;
  wasm_Table *table = kame_wasm_table();
  uint64_t owner = wasm_Table_Owner(table, (wasm_Handle)handle);
  if (owner == 0u) return NULL;
  so_R_u64_bool resolved = wasm_Table_Get(table, owner, (wasm_Handle)handle);
  if (!resolved.val2 || resolved.val >= KAME_WASM_INSTANCE_CAPACITY) return NULL;
  kame_wasm_instance *instance = &kame_wasm_instances[(uint32_t)resolved.val];
  if (!instance->live || instance->owner != owner) return NULL;
  return instance;
}

static so_String kame_wasm_request_payload(host_Request request) {
  if (request.Kind == host_RequestPlugin) return host_PayloadText(request.Payload, so_str("data"));
  if (request.Kind == host_RequestProcess || request.Kind == host_RequestPrepareOutputs) {
    if (host_PayloadText(request.Payload, so_str("data")).len != 0) return host_PayloadText(request.Payload, so_str("data"));
    if (host_PayloadStages(request.Payload).len != 0) return host_PayloadText(request.Payload, so_str("data"));
    if (host_PayloadArgv(request.Payload).len != 0) return host_PayloadText(request.Payload, so_str("data"));
    return host_PayloadText(request.Payload, so_str("script"));
  }
  if (request.Kind == host_RequestTimer || request.Kind == host_RequestProcessCancel) {
    return host_PayloadText(request.Payload, so_str("data"));
  }
  return host_PayloadPath(request.Payload);
}

static bool kame_wasm_string_eq(so_String value, const char *literal) {
  so_int len = (so_int)strlen(literal);
  if (value.len != len) return false;
  for (so_int i = 0; i < len; i++) {
    if (value.ptr[i] != literal[i]) return false;
  }
  return true;
}

/*
 * Derive the ABI request kind from the engine request. Filesystem operations
 * share host_RequestReadFile internally and carry their operation in the
 * payload, so the op field is promoted to a distinct kind. This is what lets a
 * host dispatch read, exists, stat, and glob without interpreting payloads.
 */
static uint32_t kame_wasm_request_kind(host_Request request) {
  switch (request.Kind) {
    case host_RequestReadFile: {
      so_String op = host_PayloadText(request.Payload, so_str("op"));
      if (kame_wasm_string_eq(op, "resolve-tool")) return 18u;
      if (kame_wasm_string_eq(op, "tool-exists")) return 19u;
      if (kame_wasm_string_eq(op, "output-exists")) return 17u;
      if (kame_wasm_string_eq(op, "file-times")) return 20u;
      if (kame_wasm_string_eq(op, "file-content")) return 27u;
      if (kame_wasm_string_eq(op, "tool-content")) return 28u;
      if (kame_wasm_string_eq(op, "output-content")) return 29u;
      if (kame_wasm_string_eq(op, "exists")) return 7u;
      if (kame_wasm_string_eq(op, "stat")) return 5u;
      if (kame_wasm_string_eq(op, "wildcard")) return 6u;
      return 1u;
    }
    case host_RequestWriteFile:
      return 2u;
    case host_RequestProcess:
      if (kame_wasm_string_eq(host_PayloadText(request.Payload, so_str("op")), "recipe")) return 16u;
      if (host_PayloadSetups(request.Payload).len != 0 || host_PayloadText(request.Payload, so_str("input")).len != 0 || host_PayloadText(request.Payload, so_str("output")).len != 0) return 15u;
      if (host_PayloadStages(request.Payload).len != 0) return 14u;
      return host_PayloadArgv(request.Payload).len != 0 ? 13u : 3u;
    case host_RequestEnvironment:
      return 4u;
    case host_RequestStatPath:
      return 5u;
    case host_RequestExpandGlob:
      return 6u;
    case host_RequestWallTime:
      return 8u;
    case host_RequestMonotonicTime:
      return 9u;
    case host_RequestCacheGet:
      return 10u;
    case host_RequestCachePut:
      return 11u;
    case host_RequestCacheDelete:
      return 12u;
    case host_RequestPrepareOutputs:
      return 21u;
    case host_RequestTimer:
      return 22u;
    case host_RequestProcessCancel:
      return 23u;
    case host_RequestCacheLock:
      return 24u;
    case host_RequestCacheUnlock:
      return 25u;
    case host_RequestPlugin:
      return 26u;
    default:
      return (uint32_t)request.Kind;
  }
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

/* Instance-scoped retrieval of the static emergency slot. It is valid even
 * after an out-of-memory failure because it is part of the instance record. */
uint32_t kame_wasm_instance_diagnostic_length(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return 0u;
  return instance->diagnostic_len;
}

uint32_t kame_wasm_instance_diagnostic_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (dst_len < instance->diagnostic_len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (instance->diagnostic_len == 0u) return KAME_WASM_OK;
  if (dst == 0u) return KAME_WASM_STATE_INVALID;
  uint8_t *out = (uint8_t *)(uintptr_t)dst;
  for (uint32_t i = 0; i < instance->diagnostic_len; i++) out[i] = (uint8_t)instance->diagnostic[i];
  return KAME_WASM_OK;
}

/* Copy the current diagnostic's source span into two caller-owned int32 slots.
 * Returns OK and writes the span when one is present, STATE_INVALID otherwise. */
uint32_t kame_wasm_instance_diagnostic_span(uint64_t handle, uint32_t out_start, uint32_t out_end) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->diagnostic_has_span) return KAME_WASM_STATE_INVALID;
  if (out_start == 0u || out_end == 0u) return KAME_WASM_STATE_INVALID;
  *(int32_t *)(uintptr_t)out_start = instance->diagnostic_span_start;
  *(int32_t *)(uintptr_t)out_end = instance->diagnostic_span_end;
  return KAME_WASM_OK;
}

uint64_t kame_wasm_instance_create(void) {
  KAME_WASM_CHECKPOINT(NULL, 0u, false);
  for (uint32_t i = 0; i < KAME_WASM_INSTANCE_CAPACITY; i++) {
    kame_wasm_instance *instance = &kame_wasm_instances[i];
    if (instance->live) continue;
    uint64_t owner = kame_wasm_next_token();
    wasm_Handle handle = wasm_Table_Add(kame_wasm_table(), owner, (uint64_t)i);
    if (handle == 0u) {
      kame_wasm_set_static_diagnostic("NO_MEMORY", "instance handle table is full");
      return 0u;
    }
    instance->owner = owner;
    instance->source_len = 0u;
    instance->source_name_len = 0u;
    instance->runtime = NULL;
    instance->exhausted = false;
    instance->heap_limit = KAME_WASM_ARENA_CAPACITY;
    instance->pending = (host_Request){};
    instance->parked = NULL;
    instance->has_pending = false;
    instance->event_pinned = false;
    instance->request = 0u;
    instance->last_request = 0u;
    instance->root = 0u;
    instance->node = 0u;
    instance->live = true;
    return (uint64_t)handle;
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
    while (instance->parked) {
      kame_wasm_parked_request *saved = instance->parked;
      instance->parked = saved->next;
      host_Request_Free(&saved->pending, instance->runtime->Alloc);
      mem_Free(kame_wasm_parked_request, instance->runtime->Alloc, saved);
    }
    wasm_Runtime_Free(instance->runtime);
    instance->runtime = NULL;
  }
  /* Release every handle owned by this instance, including its own. */
  wasm_Table_FreeAll(kame_wasm_table(), instance->owner);
  instance->live = false;
  instance->owner = 0u;
  instance->source_len = 0u;
  instance->has_pending = false;
  instance->event_pinned = false;
  instance->request = 0u;
  instance->last_request = 0u;
  instance->root = 0u;
  instance->node = 0u;
  return KAME_WASM_OK;
}

/* Label the compiled source for diagnostics. Must be called before
 * kame_wasm_source_compile; an unset or empty name keeps the default. */
uint32_t kame_wasm_set_source_name(uint64_t handle, uint32_t name, uint32_t name_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (name_len != 0u && name == 0u) return KAME_WASM_STATE_INVALID;
  if (name_len > sizeof(instance->source_name)) return KAME_WASM_NO_MEMORY;
  for (uint32_t i = 0; i < name_len; i++) instance->source_name[i] = ((const char *)(uintptr_t)name)[i];
  instance->source_name_len = name_len;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_source_compile(uint64_t handle, uint32_t source, uint32_t source_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, true);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if ((source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  instance->diagnostic_len = 0u;
  if (instance->has_pending || instance->parked) {
    kame_wasm_instance_set_static_diagnostic(instance, "PHASE_INVALID", "cannot compile while a host request is pending");
    return KAME_WASM_STATE_INVALID;
  }
  if (source_len > KAME_WASM_SOURCE_CAPACITY) {
    kame_wasm_instance_set_static_diagnostic(instance, "NO_MEMORY", "source exceeds instance capacity");
    return KAME_WASM_NO_MEMORY;
  }
  if (instance->arena == NULL) {
    instance->arena = (char *)(uintptr_t)kame_wasm_alloc(KAME_WASM_ARENA_CAPACITY, 16u);
    if (instance->arena == NULL) {
      kame_wasm_instance_set_static_diagnostic(instance, "NO_MEMORY", "cannot allocate instance arena");
      return KAME_WASM_NO_MEMORY;
    }
  }
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_ValidateSource(mem_System, (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic_span(instance, result.Code, result.Message, (int32_t)result.SpanStart, (int32_t)result.SpanEnd);
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
  instance->heap = wasm_NewHeap((so_Slice){(so_byte *)instance->arena, (so_int)instance->heap_limit, (so_int)instance->heap_limit});
  wasm_RuntimeStart started = wasm_NewRuntimeWithHeap(&instance->heap,
      (so_String){instance->source_name, (so_int)instance->source_name_len},
      (so_String){instance->source, (so_int)instance->source_len});
  if (started.Runtime == NULL) {
    kame_wasm_instance_set_diagnostic(instance, started.Result.Code, started.Result.Message);
    wasm_PureResult_Free(&started.Result, mem_System);
    return KAME_WASM_DIAGNOSTIC;
  }
  instance->runtime = started.Runtime;
  return KAME_WASM_OK;
}

__attribute__((export_name("kame_wasm_set_build_sources")))
uint32_t kame_wasm_set_build_sources(uint64_t handle, uint32_t data, uint32_t length) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || instance->has_pending || instance->parked || (length && !data)) return KAME_WASM_STATE_INVALID;
  if (length > 8u * KAME_WASM_SOURCE_CAPACITY) {
    kame_wasm_instance_set_static_diagnostic(instance, "NO_MEMORY", "build source descriptor exceeds instance capacity");
    return KAME_WASM_NO_MEMORY;
  }
  wasm_PureResult result = wasm_Runtime_SetBuildSources(instance->runtime, (so_Slice){(so_byte *)(uintptr_t)data, length, length});
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic_span(instance, result.Code, result.Message, result.SpanStart, result.SpanEnd);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_expression_begin(uint64_t handle, uint32_t source, uint32_t source_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  instance->diagnostic_len = 0u;
  wasm_PureResult result = wasm_Runtime_RequestExpression(instance->runtime, (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  instance->root = kame_wasm_child_handle(instance, 0u);
  instance->node = kame_wasm_child_handle(instance, 0u);
  if (instance->root == 0u || instance->node == 0u) {
    kame_wasm_instance_set_static_diagnostic(instance, "NO_MEMORY", "cannot allocate expression handles");
    return KAME_WASM_NO_MEMORY;
  }
  return KAME_WASM_OK;
}

__attribute__((export_name("kame_wasm_session_compile")))
uint32_t kame_wasm_session_compile(uint64_t handle, uint32_t data, uint32_t length) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || instance->has_pending || instance->parked || (length && !data)) return KAME_WASM_STATE_INVALID;
  wasm_PureResult result = wasm_Runtime_PrepareSession(instance->runtime, (so_Slice){(so_byte *)(uintptr_t)data, length, length});
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic_span(instance, result.Code, result.Message, result.SpanStart, result.SpanEnd);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

__attribute__((export_name("kame_wasm_register_plugins")))
uint32_t kame_wasm_register_plugins(uint64_t handle, uint32_t data, uint32_t length) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || instance->has_pending || instance->parked || (length && !data)) return KAME_WASM_STATE_INVALID;
  if (length > 1024u * 1024u) {
    kame_wasm_instance_set_static_diagnostic(instance, "PLUGIN_CONFIG", "plugin declarations exceed their byte limit");
    return KAME_WASM_NO_MEMORY;
  }
  wasm_PureResult result = wasm_Runtime_RegisterPluginsJSON(instance->runtime, (so_Slice){(so_byte *)(uintptr_t)data, length, length});
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic_span(instance, result.Code, result.Message, result.SpanStart, result.SpanEnd);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

__attribute__((export_name("kame_wasm_session_work_count")))
uint32_t kame_wasm_session_work_count(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  return instance && instance->runtime ? (uint32_t)wasm_Runtime_SessionWorkCount(instance->runtime) : 0u;
}

__attribute__((export_name("kame_wasm_session_work_kind")))
uint32_t kame_wasm_session_work_kind(uint64_t handle, uint32_t index) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  return instance && instance->runtime ? wasm_Runtime_SessionWorkKind(instance->runtime, index) : 0u;
}

__attribute__((export_name("kame_wasm_session_work_begin")))
uint32_t kame_wasm_session_work_begin(uint64_t handle, uint32_t index) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (!instance) return KAME_WASM_HANDLE_INVALID;
  if (!instance->runtime || instance->has_pending) return KAME_WASM_STATE_INVALID;
  wasm_PureResult result = wasm_Runtime_RequestSessionWork(instance->runtime, index);
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

/* Set the working directory the build runtime canonicalizes against. Must be
 * called before kame_wasm_prepare, kame_wasm_target_begin, or kame_wasm_plan. */
uint32_t kame_wasm_set_directory(uint64_t handle, uint32_t directory, uint32_t directory_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  if (directory_len != 0u && directory == 0u) return KAME_WASM_STATE_INVALID;
  bool ok = wasm_Runtime_SetDirectory(instance->runtime, (so_String){(const char *)(uintptr_t)directory, (so_int)directory_len});
  return ok ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

/* Route target host requests to the embedding host instead of the in-memory
 * filesystem. Must be called before kame_wasm_target_begin. */
uint32_t kame_wasm_set_forwarding(uint64_t handle, uint32_t enabled) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  bool ok = wasm_Runtime_SetForwarding(instance->runtime, enabled != 0u);
  return ok ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

/* Supplying an in-memory file or environment entry before kame_wasm_target_begin
 * lets a target run without any asynchronous host work. */
uint32_t kame_wasm_host_set_file(uint64_t handle, uint32_t path, uint32_t path_len, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  if ((path_len != 0u && path == 0u) || (data_len != 0u && data == 0u)) return KAME_WASM_STATE_INVALID;
  bool ok = wasm_Runtime_SetFile(instance->runtime,
      (so_String){(const char *)(uintptr_t)path, (so_int)path_len},
      (so_Slice){(so_byte *)(uintptr_t)data, (so_int)data_len, (so_int)data_len});
  return ok ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

uint32_t kame_wasm_host_set_env(uint64_t handle, uint32_t name, uint32_t name_len, uint32_t value, uint32_t value_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  if ((name_len != 0u && name == 0u) || (value_len != 0u && value == 0u)) return KAME_WASM_STATE_INVALID;
  bool ok = wasm_Runtime_SetEnvironment(instance->runtime,
      (so_String){(const char *)(uintptr_t)name, (so_int)name_len},
      (so_String){(const char *)(uintptr_t)value, (so_int)value_len});
  return ok ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

/* Schedule one target through the portable build runtime. The active root is a
 * target rather than an expression; step, result, and diagnostics are shared. */
uint32_t kame_wasm_target_begin(uint64_t handle, uint32_t target, uint32_t target_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || (target_len != 0u && target == 0u)) return KAME_WASM_STATE_INVALID;
  instance->diagnostic_len = 0u;
  wasm_PureResult result = wasm_Runtime_RequestTarget(instance->runtime, (so_String){(const char *)(uintptr_t)target, (so_int)target_len});
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_watch_cancel(uint64_t handle) {
  return kame_wasm_expression_cancel(handle);
}

static uint32_t kame_wasm_watch_batch(uint64_t handle, uint32_t data, uint32_t len, bool start) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || (len != 0u && data == 0u)) return KAME_WASM_STATE_INVALID;
  instance->diagnostic_len = 0u;
  so_Slice bytes = {(so_byte *)(uintptr_t)data, (so_int)len, (so_int)len};
  wasm_PureResult result = start ? wasm_Runtime_RequestWatch(instance->runtime, bytes)
                               : wasm_Runtime_InvalidateWatch(instance->runtime, bytes);
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_watch_begin(uint64_t handle, uint32_t data, uint32_t len) {
  return kame_wasm_watch_batch(handle, data, len, true);
}

uint32_t kame_wasm_watch_invalidate(uint64_t handle, uint32_t data, uint32_t len) {
  return kame_wasm_watch_batch(handle, data, len, false);
}

/* Copy a nonmutating snapshot of retained roots and observed file/glob keys. */
uint32_t kame_wasm_watch_state(uint64_t handle, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  wasm_PureResult result = wasm_Runtime_WatchStateJSON(instance->runtime);
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  uint32_t needed = (uint32_t)result.Text.len;
  *(uint32_t *)(uintptr_t)out_len = needed;
  if (dst_len < needed) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (needed != 0u && dst == 0u) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)result.Text.ptr[i];
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

/* Pop one queued target lifecycle event and copy its schema-1 JSON line. A
 * zero-length result means no event is pending. */
uint32_t kame_wasm_target_event(uint64_t handle, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  if (wasm_Runtime_EventJSONLength(instance->runtime) == 0) {
    wasm_Runtime_NextEventJSON(instance->runtime);
  }
  uint32_t needed = (uint32_t)wasm_Runtime_EventJSONLength(instance->runtime);
  *(uint32_t *)(uintptr_t)out_len = needed;
  if (needed == 0u) return KAME_WASM_OK;
  if (dst_len < needed) return KAME_WASM_BUFFER_TOO_SMALL;
  if (dst == 0u) return KAME_WASM_STATE_INVALID;
  if (!wasm_Runtime_EventJSONCopy(instance->runtime,
      (so_Slice){(so_byte *)(uintptr_t)dst, (so_int)needed, (so_int)needed})) {
    return KAME_WASM_STATE_INVALID;
  }
  return KAME_WASM_OK;
}

uint32_t kame_wasm_process_started(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || !instance->has_pending) return KAME_WASM_STATE_INVALID;
  wasm_Runtime_ProcessStarted(instance->runtime, instance->pending);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_process_stream(uint64_t handle, uint32_t stderr, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || !instance->has_pending) return KAME_WASM_STATE_INVALID;
  if (data_len != 0u && data == 0u) return KAME_WASM_STATE_INVALID;
  wasm_Runtime_ProcessStream(instance->runtime, instance->pending, stderr != 0u,
      (so_Slice){(so_byte *)(uintptr_t)data, (so_int)data_len, (so_int)data_len});
  return KAME_WASM_OK;
}

uint32_t kame_wasm_process_terminal(uint64_t handle, int32_t status, int32_t signal, uint32_t outcome,
    uint32_t stdout_, uint32_t stdout_len, uint32_t stderr_, uint32_t stderr_len,
    uint32_t code, uint32_t code_len, uint32_t message, uint32_t message_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || !instance->has_pending) return KAME_WASM_STATE_INVALID;
  if (stdout_len != 0u && stdout_ == 0u) return KAME_WASM_STATE_INVALID;
  if (stderr_len != 0u && stderr_ == 0u) return KAME_WASM_STATE_INVALID;
  wasm_Runtime_ProcessTerminal(instance->runtime, instance->pending,
      (so_Slice){(so_byte *)(uintptr_t)stdout_, (so_int)stdout_len, (so_int)stdout_len},
      (so_Slice){(so_byte *)(uintptr_t)stderr_, (so_int)stderr_len, (so_int)stderr_len},
      (so_int)status, (so_int)signal, (so_int)outcome,
      (so_String){(const char *)(uintptr_t)code, (so_int)code_len},
      (so_String){(const char *)(uintptr_t)message, (so_int)message_len});
  host_Request_Free(&instance->pending, instance->runtime->Alloc);
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  instance->request = 0u;
  return KAME_WASM_OK;
}

/* Copy the declared build tool names as a JSON array. */
uint32_t kame_wasm_tools(uint64_t handle, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  wasm_PureResult result = wasm_Runtime_ToolNames(instance->runtime);
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  uint32_t needed = (uint32_t)result.Text.len;
  *(uint32_t *)(uintptr_t)out_len = needed;
  if (dst_len < needed) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (needed != 0u && dst == 0u) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)result.Text.ptr[i];
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_set_tool_path(uint64_t handle, uint32_t name, uint32_t name_len, uint32_t path, uint32_t path_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || name_len == 0u || name == 0u || (path_len != 0u && path == 0u)) return KAME_WASM_STATE_INVALID;
  return wasm_Runtime_SetToolPath(instance->runtime,
      (so_String){(const char *)(uintptr_t)name, (so_int)name_len},
      (so_String){(const char *)(uintptr_t)path, (so_int)path_len}) ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

uint32_t kame_wasm_tools_check(uint64_t handle, uint32_t target, uint32_t target_len, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u || (target_len != 0u && target == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  if (instance->has_pending) return KAME_WASM_HOST_NEEDED;
  instance->diagnostic_len = 0u;
  wasm_PureResult result = wasm_Runtime_ToolsCheckJSON(instance->runtime, (so_String){(const char *)(uintptr_t)target, (so_int)target_len});
  if (result.HostNeeded) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_HOST_NEEDED;
  }
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  uint32_t needed = (uint32_t)result.Text.len;
  *(uint32_t *)(uintptr_t)out_len = needed;
  if (dst_len < needed) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (needed != 0u && dst == 0u) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)result.Text.ptr[i];
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_inspection_grant(uint64_t handle, uint32_t capability, uint32_t capability_len, uint32_t name, uint32_t name_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || (capability_len != 0u && capability == 0u) || (name_len != 0u && name == 0u)) return KAME_WASM_STATE_INVALID;
  return wasm_Runtime_InspectionGrant(instance->runtime,
      (so_String){(const char *)(uintptr_t)capability, (so_int)capability_len},
      (so_String){(const char *)(uintptr_t)name, (so_int)name_len}) ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

/* Walk one target's declared inputs/outputs (kind 0/1) or span (kind 2). */
uint32_t kame_wasm_graph(uint64_t handle, uint32_t target, uint32_t target_len, int32_t depth, uint32_t kind, uint32_t expand, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u || (target_len != 0u && target == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  if (instance->has_pending) return KAME_WASM_HOST_NEEDED;
  instance->diagnostic_len = 0u;
  so_String name = (so_String){(const char *)(uintptr_t)target, (so_int)target_len};
  wasm_PureResult result;
  if (kind == 2u) {
    result = wasm_Runtime_SpanJSON(instance->runtime, name, (so_int)depth, expand != 0u);
  } else {
    result = wasm_Runtime_GraphJSON(instance->runtime, name, (so_int)depth, kind == 0u ? so_str("inputs") : so_str("outputs"));
  }
  if (result.HostNeeded) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_HOST_NEEDED;
  }
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  uint32_t needed = (uint32_t)result.Text.len;
  *(uint32_t *)(uintptr_t)out_len = needed;
  if (dst_len < needed) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (needed != 0u && dst == 0u) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)result.Text.ptr[i];
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

/* Compile the instance source into a build runtime for planning/inspection. */
uint32_t kame_wasm_prepare(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  instance->diagnostic_len = 0u;
  wasm_PureResult result = wasm_Runtime_Prepare(instance->runtime);
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

/* Resolve one target plan and copy its schema-1 JSON into caller memory. */
uint32_t kame_wasm_plan(uint64_t handle, uint32_t target, uint32_t target_len, uint32_t expand, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u || (target_len != 0u && target == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  wasm_PureResult result = wasm_Runtime_PlanJSON(instance->runtime, (so_String){(const char *)(uintptr_t)target, (so_int)target_len}, expand != 0u);
  if (result.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Code, result.Message);
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  uint32_t needed = (uint32_t)result.Text.len;
  *(uint32_t *)(uintptr_t)out_len = needed;
  if (dst_len < needed) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (needed != 0u && dst == 0u) {
    wasm_PureResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  for (uint32_t i = 0; i < needed; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)result.Text.ptr[i];
  wasm_PureResult_Free(&result, instance->runtime->Alloc);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_expression_cancel(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || !wasm_Runtime_Cancel(instance->runtime)) return KAME_WASM_STATE_INVALID;
  if (instance->request != 0u) instance->last_request = instance->request;
  instance->request = 0u;
  if (instance->has_pending) {
    host_Request_Free(&instance->pending, instance->runtime->Alloc);
    instance->pending = (host_Request){};
    instance->has_pending = false;
  }
  instance->event_pinned = false;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_expression_effect_kind(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || instance->runtime == NULL) return 0u;
  return wasm_Runtime_ExpressionEffectKind(instance->runtime);
}

uint32_t kame_wasm_expression_effect_length(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || instance->runtime == NULL) return 0u;
  return (uint32_t)wasm_Runtime_ExpressionEffectLength(instance->runtime);
}

uint32_t kame_wasm_expression_effect_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  uint32_t needed = (uint32_t)wasm_Runtime_ExpressionEffectLength(instance->runtime);
  if (wasm_Runtime_ExpressionEffectKind(instance->runtime) == 0u) return KAME_WASM_STATE_INVALID;
  if (dst_len < needed) return KAME_WASM_BUFFER_TOO_SMALL;
  if (needed != 0u && dst == 0u) return KAME_WASM_STATE_INVALID;
  return wasm_Runtime_CopyExpressionEffect(instance->runtime,
      (so_Slice){(so_byte *)(uintptr_t)dst, (so_int)dst_len, (so_int)dst_len}) ? KAME_WASM_OK : KAME_WASM_STATE_INVALID;
}

uint32_t kame_wasm_step(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, 2u, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL) return KAME_WASM_STATE_INVALID;
  if (instance->has_pending) return 1u;
  host_NextResult next = wasm_Runtime_Step(instance->runtime);
  if (next.OK) {
    instance->pending = next.Request;
    instance->has_pending = true;
    instance->event_pinned = true;
    instance->request = kame_wasm_child_handle(instance, (uint64_t)next.Request.ID);
    instance->last_request = instance->request;
    if (instance->request == 0u) {
      kame_wasm_instance_set_static_diagnostic(instance, "NO_MEMORY", "cannot allocate request handle");
    }
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
  if (!instance->has_pending || !instance->event_pinned || instance->request == 0u) return KAME_WASM_STATE_INVALID;
  so_String payload = kame_wasm_request_payload(instance->pending);
  return kame_wasm_event_header(dst, dst_len, 1u, instance->root, instance->node,
      instance->request, instance->pending.Generation, 0, (uint32_t)payload.len);
}

uint32_t kame_wasm_next_request_kind(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return 0u;
  if (!instance->has_pending || !instance->event_pinned) return 0u;
  return kame_wasm_request_kind(instance->pending);
}

uint32_t kame_wasm_next_request_data_length(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || !instance->has_pending || !instance->event_pinned) return 0u;
  if (instance->pending.Kind != host_RequestWriteFile) return 0u;
  return (uint32_t)host_PayloadBytes(instance->pending.Payload, so_str("data")).len;
}

uint32_t kame_wasm_request_data_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned || instance->pending.Kind != host_RequestWriteFile) return KAME_WASM_STATE_INVALID;
  so_Slice data = host_PayloadBytes(instance->pending.Payload, so_str("data"));
  if (dst_len < (uint32_t)data.len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (data.len != 0 && dst == 0u) return KAME_WASM_STATE_INVALID;
  const uint8_t *bytes = (const uint8_t *)data.ptr;
  for (so_int i = 0; i < data.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = bytes[i];
  return KAME_WASM_OK;
}

static bool kame_wasm_is_cache_request(host_RequestKind kind) {
  return kind == host_RequestCacheGet || kind == host_RequestCachePut || kind == host_RequestCacheDelete || kind == host_RequestCacheLock || kind == host_RequestCacheUnlock;
}

/* Copy the opaque cache key of a pinned cache request. */
uint32_t kame_wasm_next_request_key_length(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || !instance->has_pending || !instance->event_pinned) return 0u;
  if (!kame_wasm_is_cache_request(instance->pending.Kind)) return 0u;
  return (uint32_t)host_CacheKey(instance->pending.Payload).len;
}

uint32_t kame_wasm_request_key_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned || !kame_wasm_is_cache_request(instance->pending.Kind)) return KAME_WASM_STATE_INVALID;
  so_Slice key = host_CacheKey(instance->pending.Payload);
  if (dst_len < (uint32_t)key.len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (key.len != 0 && dst == 0u) return KAME_WASM_STATE_INVALID;
  const uint8_t *bytes = (const uint8_t *)key.ptr;
  for (so_int i = 0; i < key.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = bytes[i];
  return KAME_WASM_OK;
}

/* Copy the opaque record carried by a pinned cache put request. */
uint32_t kame_wasm_next_request_record_length(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || !instance->has_pending || !instance->event_pinned) return 0u;
  if (instance->pending.Kind != host_RequestCachePut) return 0u;
  return (uint32_t)host_CacheRecord(instance->pending.Payload).len;
}

uint32_t kame_wasm_request_record_copy(uint64_t handle, uint32_t dst, uint32_t dst_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (!instance->has_pending || !instance->event_pinned || instance->pending.Kind != host_RequestCachePut) return KAME_WASM_STATE_INVALID;
  so_Slice record = host_CacheRecord(instance->pending.Payload);
  if (dst_len < (uint32_t)record.len) return KAME_WASM_BUFFER_TOO_SMALL;
  if (record.len != 0 && dst == 0u) return KAME_WASM_STATE_INVALID;
  const uint8_t *bytes = (const uint8_t *)record.ptr;
  for (so_int i = 0; i < record.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = bytes[i];
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

/*
 * Resolve a completion's request handle. The handle must belong to this
 * instance. A handle matching the request that is currently pinned is applied;
 * a completion for the most recent request after it completed or was cancelled
 * is accepted and ignored, as docs/spec/010-wasm.md requires. Any other handle
 * is foreign or stale.
 */
uint32_t kame_wasm_request_detach(uint64_t handle, uint64_t request);

static uint32_t kame_wasm_completion_target(kame_wasm_instance *instance, uint64_t request, bool *pending) {
  *pending = false;
  if (request == 0u) return KAME_WASM_HANDLE_INVALID;
  wasm_Table *table = kame_wasm_table();
  uint64_t owner = wasm_Table_Owner(table, (wasm_Handle)request);
  if (owner == 0u || owner != instance->owner) return KAME_WASM_HANDLE_INVALID;
  so_R_u64_bool resolved = wasm_Table_Get(table, owner, (wasm_Handle)request);
  if (!resolved.val2) return KAME_WASM_HANDLE_INVALID;
  kame_wasm_parked_request **at = &instance->parked;
  while (*at && (*at)->request != request) at = &(*at)->next;
  if (*at) {
    kame_wasm_parked_request *saved = *at;
    *at = saved->next;
    if (instance->has_pending) {
      kame_wasm_parked_request *active = mem_Alloc(kame_wasm_parked_request, instance->runtime->Alloc);
      active->pending = instance->pending;
      active->request = instance->request;
      active->next = instance->parked;
      instance->parked = active;
    }
    instance->pending = saved->pending;
    instance->request = request;
    instance->has_pending = true;
    mem_Free(kame_wasm_parked_request, instance->runtime->Alloc, saved);
  }
  if (instance->has_pending) {
    if (request != instance->request) return KAME_WASM_HANDLE_INVALID;
    *pending = true;
    return KAME_WASM_OK;
  }
  if (request != instance->last_request) return KAME_WASM_HANDLE_INVALID;
  return KAME_WASM_OK;
}

__attribute__((export_name("kame_wasm_request_attach")))
uint32_t kame_wasm_request_attach(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (!instance) return KAME_WASM_HANDLE_INVALID;
  bool pending = false;
  uint32_t status = kame_wasm_completion_target(instance, request, &pending);
  if (status != KAME_WASM_OK || !pending) return KAME_WASM_HANDLE_INVALID;
  instance->event_pinned = true;
  return KAME_WASM_OK;
}

static void kame_wasm_clear_completion(kame_wasm_instance *instance) {
  host_Request_Free(&instance->pending, instance->runtime->Alloc);
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  instance->request = 0u;
}

/* Unpin a launched process so the same instance can service other nodes. */
__attribute__((export_name("kame_wasm_request_detach")))
uint32_t kame_wasm_request_detach(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (!instance || !instance->runtime || !instance->has_pending || instance->request != request) return KAME_WASM_STATE_INVALID;
  kame_wasm_parked_request *saved = mem_Alloc(kame_wasm_parked_request, instance->runtime->Alloc);
  saved->pending = instance->pending;
  saved->request = request;
  saved->next = instance->parked;
  instance->parked = saved;
  instance->pending = (host_Request){};
  instance->has_pending = false;
  instance->event_pinned = false;
  instance->request = 0u;
  return KAME_WASM_OK;
}

/* Return the portable retention budget for a pending or detached process.
 * UINT32_MAX denotes an invalid request rather than an unlimited budget. */
__attribute__((export_name("kame_wasm_process_retain_limit")))
uint32_t kame_wasm_process_retain_limit(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (!instance || !instance->runtime) return UINT32_MAX;
  host_Request *pending = NULL;
  if (instance->has_pending && (request == 0u || instance->request == request)) pending = &instance->pending;
  for (kame_wasm_parked_request *saved = instance->parked; saved && !pending; saved = saved->next) {
    if (saved->request == request) pending = &saved->pending;
  }
  if (!pending) return UINT32_MAX;
  return (uint32_t)wasm_Runtime_ProcessRetainLimit(instance->runtime, *pending);
}

__attribute__((export_name("kame_wasm_process_stream_request")))
uint32_t kame_wasm_process_stream_request(uint64_t handle, uint64_t request, uint32_t stderr, uint32_t data, uint32_t length) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (!instance || !instance->runtime || (length && !data)) return KAME_WASM_STATE_INVALID;
  host_Request *pending = NULL;
  if (instance->has_pending && instance->request == request) pending = &instance->pending;
  for (kame_wasm_parked_request *saved = instance->parked; saved && !pending; saved = saved->next) {
    if (saved->request == request) pending = &saved->pending;
  }
  if (!pending) return KAME_WASM_HANDLE_INVALID;
  wasm_Runtime_ProcessStream(instance->runtime, *pending, stderr != 0u, (so_Slice){(so_byte *)(uintptr_t)data, (so_int)length, (so_int)length});
  return KAME_WASM_OK;
}

__attribute__((export_name("kame_wasm_process_started_request")))
uint32_t kame_wasm_process_started_request(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (!instance || !instance->runtime) return KAME_WASM_STATE_INVALID;
  for (kame_wasm_parked_request *saved = instance->parked; saved; saved = saved->next) {
    if (saved->request == request) { wasm_Runtime_ProcessStarted(instance->runtime, saved->pending); return KAME_WASM_OK; }
  }
  return KAME_WASM_HANDLE_INVALID;
}

__attribute__((export_name("kame_wasm_process_exited_request")))
uint32_t kame_wasm_process_exited_request(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (!instance || !instance->runtime) return KAME_WASM_STATE_INVALID;
  for (kame_wasm_parked_request *saved = instance->parked; saved; saved = saved->next) {
    if (saved->request == request) { wasm_Runtime_ProcessExited(instance->runtime, saved->pending); return KAME_WASM_OK; }
  }
  return KAME_WASM_HANDLE_INVALID;
}

uint32_t kame_wasm_complete_bytes(uint64_t handle, uint64_t request, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  bool pending = false;
  uint32_t status = kame_wasm_completion_target(instance, request, &pending);
  if (status != KAME_WASM_OK) return status;
  if (!pending) return KAME_WASM_OK;
  if (data_len != 0u && data == 0u) return KAME_WASM_STATE_INVALID;
  core_Value value = core_NewBytes(instance->runtime->Alloc, (so_Slice){(so_byte *)(uintptr_t)data, (so_int)data_len, (so_int)data_len});
  wasm_Runtime_Complete(instance->runtime, instance->pending, value, (diagnostic_Diagnostic){});
  kame_wasm_clear_completion(instance);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_text(uint64_t handle, uint64_t request, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  bool pending = false;
  uint32_t status = kame_wasm_completion_target(instance, request, &pending);
  if (status != KAME_WASM_OK) return status;
  if (!pending) return KAME_WASM_OK;
  if (data_len != 0u && data == 0u) return KAME_WASM_STATE_INVALID;
  core_Value value = core_NewString(instance->runtime->Alloc, (so_String){(const char *)(uintptr_t)data, (so_int)data_len});
  wasm_Runtime_Complete(instance->runtime, instance->pending, value, (diagnostic_Diagnostic){});
  kame_wasm_clear_completion(instance);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_nil(uint64_t handle, uint64_t request) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  bool pending = false;
  uint32_t status = kame_wasm_completion_target(instance, request, &pending);
  if (status != KAME_WASM_OK) return status;
  if (!pending) return KAME_WASM_OK;
  wasm_Runtime_Complete(instance->runtime, instance->pending, (core_Value){}, (diagnostic_Diagnostic){});
  kame_wasm_clear_completion(instance);
  return KAME_WASM_OK;
}

uint32_t kame_wasm_complete_failure(uint64_t handle, uint64_t request,
                                    uint32_t code, uint32_t code_len,
                                    uint32_t message, uint32_t message_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  bool pending = false;
  uint32_t status = kame_wasm_completion_target(instance, request, &pending);
  if (status != KAME_WASM_OK) return status;
  if (!pending) return KAME_WASM_OK;
  if ((code_len != 0u && code == 0u) || (message_len != 0u && message == 0u)) return KAME_WASM_STATE_INVALID;
  diagnostic_Diagnostic diagnostic = (diagnostic_Diagnostic){
      .Severity = diagnostic_Error,
      .Code = (so_String){(const char *)(uintptr_t)code, (so_int)code_len},
      .Message = (so_String){(const char *)(uintptr_t)message, (so_int)message_len},
  };
  wasm_Runtime_Complete(instance->runtime, instance->pending, (core_Value){}, diagnostic);
  kame_wasm_clear_completion(instance);
  return KAME_WASM_OK;
}

/* Complete a structured request with a canonical JSON value. Records, lists,
 * booleans, integers, floats, strings, and null are accepted; the engine owns
 * the parsed value. Invalid JSON fails the pending request with HOST_FAIL. */
uint32_t kame_wasm_complete_json(uint64_t handle, uint64_t request, uint32_t data, uint32_t data_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  bool pending = false;
  uint32_t status = kame_wasm_completion_target(instance, request, &pending);
  if (status != KAME_WASM_OK) return status;
  if (!pending) return KAME_WASM_OK;
  if (data_len != 0u && data == 0u) return KAME_WASM_STATE_INVALID;
  core_Value value = (core_Value){};
  so_Slice json = (so_Slice){(so_byte *)(uintptr_t)data, (so_int)data_len, (so_int)data_len};
  if (!wasm_CompletionValueFromJSON(instance->runtime->Alloc, json, &value)) {
    kame_wasm_instance_set_static_diagnostic(instance, "HOST_FAIL", "host completion is not valid canonical JSON");
    return KAME_WASM_DIAGNOSTIC;
  }
  wasm_Runtime_Complete(instance->runtime, instance->pending, value, (diagnostic_Diagnostic){});
  kame_wasm_clear_completion(instance);
  return KAME_WASM_OK;
}

/* Shape of a completed target: 0 none, 1 definition value, 2 file path. */
uint32_t kame_wasm_result_kind(uint64_t handle) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL || instance->runtime == NULL) return 0u;
  return wasm_Runtime_TargetResultKind(instance->runtime);
}

uint32_t kame_wasm_result_copy(uint64_t handle, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, false);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime == NULL || out_len == 0u) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  wasm_RuntimeResult result = wasm_Runtime_Result(instance->runtime);
  if (!result.Done) {
    wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_STATE_INVALID;
  }
  if (result.Diagnostic.Code.len != 0) {
    kame_wasm_instance_set_diagnostic(instance, result.Diagnostic.Code, result.Diagnostic.Message);
    wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
    return KAME_WASM_DIAGNOSTIC;
  }
  // A kind-2 result is a file artifact path, not an expression value, so it is
  // returned verbatim for the caller to read. Every other result renders
  // exactly as the native CLI writes a value to stdout.
  so_String text;
  if (wasm_Runtime_TargetResultKind(instance->runtime) == 2u) {
    so_Slice buffer = mem_AllocSlice(char, instance->runtime->Alloc, result.Value.Text.len, result.Value.Text.len);
    char *bytes = (char *)buffer.ptr;
    for (so_int i = 0; i < result.Value.Text.len; i++) bytes[i] = result.Value.Text.ptr[i];
    text = (so_String){bytes, result.Value.Text.len};
  } else {
    text = eval_Display(instance->runtime->Alloc, result.Value);
  }
  wasm_RuntimeResult_Free(&result, instance->runtime->Alloc);
  *(uint32_t *)(uintptr_t)out_len = (uint32_t)text.len;
  if (dst_len < (uint32_t)text.len) {
    mem_FreeString(instance->runtime->Alloc, text);
    return KAME_WASM_BUFFER_TOO_SMALL;
  }
  if (text.len != 0 && dst == 0u) {
    mem_FreeString(instance->runtime->Alloc, text);
    return KAME_WASM_STATE_INVALID;
  }
  for (so_int i = 0; i < text.len; i++) ((uint8_t *)(uintptr_t)dst)[i] = (uint8_t)text.ptr[i];
  mem_FreeString(instance->runtime->Alloc, text);
  return KAME_WASM_OK;
}

static uint32_t kame_wasm_finish_pure(size_t mark, wasm_PureResult result,
                                      uint32_t dst, uint32_t dst_len, uint32_t out_len,
                                      char *diag, uint32_t diag_cap, uint32_t *diag_len) {
  if (result.Code.len != 0) {
    kame_wasm_write_diagnostic(diag, diag_cap, diag_len, result.Code, result.Message);
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

/* Parse one language source and return its schema-1 AST JSON. Parse errors are
 * embedded in the JSON diagnostics, matching the native `do parse` output. */
uint32_t kame_wasm_parse(uint64_t handle, uint32_t lang, uint32_t lang_len,
                         uint32_t name, uint32_t name_len,
                         uint32_t text, uint32_t text_len,
                         uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, true);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (out_len == 0u || (lang_len != 0u && lang == 0u) || (name_len != 0u && name == 0u) || (text_len != 0u && text == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_ParseLanguage(
      mem_System,
      (so_String){(const char *)(uintptr_t)lang, (so_int)lang_len},
      (so_String){(const char *)(uintptr_t)name, (so_int)name_len},
      (so_String){(const char *)(uintptr_t)text, (so_int)text_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len, instance->diagnostic, (uint32_t)sizeof(instance->diagnostic), &instance->diagnostic_len);
}

/* Format one language source and copy the canonical text into caller-owned
 * memory. A parse failure reports the first diagnostic through the instance. */
uint32_t kame_wasm_format(uint64_t handle, uint32_t lang, uint32_t lang_len,
                          uint32_t name, uint32_t name_len,
                          uint32_t text, uint32_t text_len,
                          uint32_t indent, uint32_t indent_len, uint32_t width,
                          uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, true);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (out_len == 0u || (lang_len != 0u && lang == 0u) || (name_len != 0u && name == 0u) || (text_len != 0u && text == 0u) || (indent_len != 0u && indent == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_FormatLanguage(
      mem_System,
      (so_String){(const char *)(uintptr_t)lang, (so_int)lang_len},
      (so_String){(const char *)(uintptr_t)name, (so_int)name_len},
      (so_String){(const char *)(uintptr_t)text, (so_int)text_len},
      (so_String){(const char *)(uintptr_t)indent, (so_int)indent_len},
      (so_int)width);
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len, instance->diagnostic, (uint32_t)sizeof(instance->diagnostic), &instance->diagnostic_len);
}

/* Parse one command line with the shared CLI grammar and return its
 * Invocation JSON. Arguments are NUL-separated; command is "" for the primary
 * invocation. This is the single grammar shared with the native CLI. */
uint32_t kame_wasm_cli(uint64_t handle, uint32_t command, uint32_t command_len, uint32_t args, uint32_t args_len, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, true);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (out_len == 0u || (command_len != 0u && command == 0u) || (args_len != 0u && args == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_ParseCLI(
      mem_System,
      (so_String){(const char *)(uintptr_t)command, (so_int)command_len},
      (so_String){(const char *)(uintptr_t)args, (so_int)args_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len, instance->diagnostic, (uint32_t)sizeof(instance->diagnostic), &instance->diagnostic_len);
}

uint32_t kame_wasm_eval_pure(uint32_t source, uint32_t source_len,
                             uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  KAME_WASM_CHECKPOINT(NULL, KAME_WASM_NO_MEMORY, true);
  if (out_len == 0u || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  kame_wasm_diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_EvaluatePure(mem_System, (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len,
      kame_wasm_diagnostic, (uint32_t)sizeof(kame_wasm_diagnostic), &kame_wasm_diagnostic_len);
}

uint32_t kame_wasm_eval_source_pure(uint32_t program, uint32_t program_len,
                                    uint32_t source, uint32_t source_len,
                                    uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  KAME_WASM_CHECKPOINT(NULL, KAME_WASM_NO_MEMORY, true);
  if (out_len == 0u || (program_len != 0u && program == 0u) || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  kame_wasm_diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_EvaluateSourcePure(
      mem_System,
      (so_String){(const char *)(uintptr_t)program, (so_int)program_len},
      (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len,
      kame_wasm_diagnostic, (uint32_t)sizeof(kame_wasm_diagnostic), &kame_wasm_diagnostic_len);
}

uint32_t kame_wasm_expression_request(uint64_t handle, uint32_t source,
                                      uint32_t source_len, uint32_t dst,
                                      uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, true);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (out_len == 0u || (source_len != 0u && source == 0u)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_EvaluateSourcePure(
      mem_System,
      (so_String){instance->source, (so_int)instance->source_len},
      (so_String){(const char *)(uintptr_t)source, (so_int)source_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len,
      instance->diagnostic, (uint32_t)sizeof(instance->diagnostic), &instance->diagnostic_len);
}

/* Hosts can choose a smaller fixed logical heap before compilation. */
__attribute__((export_name("kame_wasm_instance_set_heap_limit")))
uint32_t kame_wasm_instance_set_heap_limit(uint64_t handle, uint32_t limit) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (instance->runtime != NULL || instance->exhausted || limit == 0u || limit > KAME_WASM_ARENA_CAPACITY) return KAME_WASM_STATE_INVALID;
  instance->heap_limit = limit;
  return KAME_WASM_OK;
}

uint32_t kame_wasm_declaration_predicate(uint64_t handle, uint32_t data,
    uint32_t data_len, uint32_t dst, uint32_t dst_len, uint32_t out_len) {
  kame_wasm_instance *instance = kame_wasm_instance_get(handle);
  KAME_WASM_CHECKPOINT(instance, KAME_WASM_NO_MEMORY, true);
  if (instance == NULL) return KAME_WASM_HANDLE_INVALID;
  if (out_len == 0u || (data_len && !data)) return KAME_WASM_STATE_INVALID;
  *(uint32_t *)(uintptr_t)out_len = 0u;
  instance->diagnostic_len = 0u;
  size_t mark = so_heap_mark();
  wasm_PureResult result = wasm_DeclarationPredicate(mem_System,
      (so_Slice){(so_byte *)(uintptr_t)data, data_len, data_len});
  return kame_wasm_finish_pure(mark, result, dst, dst_len, out_len,
      instance->diagnostic, (uint32_t)sizeof(instance->diagnostic), &instance->diagnostic_len);
}
