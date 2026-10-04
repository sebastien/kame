#ifndef KAME_WASM_PANIC_H
#define KAME_WASM_PANIC_H
#include "so/builtin/builtin.h"

/* Only the freestanding ABI installs an allocation-failure boundary. */
_Noreturn void kame_wasm_panic(const char *message);
#undef so_panic
#define so_panic(message) kame_wasm_panic(message)
#endif
