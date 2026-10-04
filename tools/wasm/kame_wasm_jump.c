#include <stdint.h>

/* LLVM's native WebAssembly SjLj lowering uses this private tag and the
 * owner/label/argument layout below. No JavaScript exception helpers are needed.
 * ABI reference: Emscripten compiler-rt/emscripten_setjmp.c. */
__asm__(".globl __c_longjmp\n.tagtype __c_longjmp i32\n__c_longjmp:\n");
struct kame_jump_state {
  void *owner;
  uint32_t label;
  void *environment;
  int value;
};
void __wasm_setjmp(void *environment, uint32_t label, void *owner) {
  struct kame_jump_state *state = environment;
  state->owner = owner;
  state->label = label;
}
uint32_t __wasm_setjmp_test(void *environment, void *owner) {
  struct kame_jump_state *state = environment;
  return state->owner == owner ? state->label : 0;
}
_Noreturn void __wasm_longjmp(void *environment, int value) {
  struct kame_jump_state *state = environment;
  state->environment = environment;
  state->value = value ? value : 1;
  __builtin_wasm_throw(1, &state->environment);
}
