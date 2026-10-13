/* Standalone public ABI check; no generated Solod headers or runtime needed.
 * cc -std=c11 -Wall -Wextra -Werror -fsyntax-only tools/native/test-wasm-header.c
 */
#include "../../src/go/kame/host/wasm/kame_wasm.h"

_Static_assert(KAME_WASM_OK == 0, "OK status");
_Static_assert(KAME_WASM_HANDLE_INVALID == 1, "invalid-handle status");
_Static_assert(KAME_WASM_NO_MEMORY == 2, "out-of-memory status");
_Static_assert(KAME_WASM_BUFFER_TOO_SMALL == 3, "short-buffer status");
_Static_assert(KAME_WASM_STATE_INVALID == 4, "invalid-state status");
_Static_assert(KAME_WASM_DIAGNOSTIC == 5, "diagnostic status");
_Static_assert(KAME_WASM_HOST_NEEDED == 6, "host-needed status");
_Static_assert(KAME_WASM_REQUEST_EXPAND_GLOB == 6, "glob request");
_Static_assert(KAME_WASM_REQUEST_PLUGIN == 26, "existing request boundary");
_Static_assert(KAME_WASM_REQUEST_FILE_CONTENT == 27, "file-content request");
_Static_assert(KAME_WASM_REQUEST_TOOL_CONTENT == 28, "tool-content request");
_Static_assert(KAME_WASM_REQUEST_OUTPUT_CONTENT == 29, "output-content request");
_Static_assert(KAME_WASM_REQUEST_LSTAT == 30, "lstat request");
_Static_assert(KAME_WASM_REQUEST_READLINK == 31, "readlink request");
