package main

import (
	"kame/host/wasm"
	"kame/internal/mem"
)

//go:wasmexport kame_wasm_present
func present(handle uint64, src, length, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if length > 1024*1024 {
		return 4
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	r := wasm.PureResult{}
	if i.presentation != nil {
		r = i.presentation.Render(s)
	} else {
		r = wasm.RenderPresentation(mem.System, s)
	}
	return finish(r, dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_present_observe
func presentObserve(handle, scope uint64, now int64, src, length uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if i.runtime != nil || now < 0 || length > 1024*1024 {
		return 4
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	created := i.presentation == nil
	if created {
		i.presentation = wasm.NewPresentation(mem.System)
	}
	r := i.presentation.Observe(s, int64(scope), now)
	if created && r.Code != "" {
		i.presentation.Free()
		i.presentation = nil
	}
	return i.status(r)
}

//go:wasmexport kame_wasm_declaration_predicate
func predicate(handle uint64, src, length, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return finish(wasm.DeclarationPredicate(mem.System, s), dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_composition_condition
func composition(handle uint64, src, length, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	r := wasm.RunCompositionCondition(mem.System, s)
	if r.Code != "" && r.Text != "" {
		if len(r.Text) > 65536 {
			i.diagnostic = "NO_MEMORY: composition diagnostic exceeds 65536-byte source capacity"
			return 2
		}
		i.diagnostic = r.Text
		return 5
	}
	return finish(r, dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_target_event
func targetEvent(handle uint64, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.runtime.EventJSONLength() == 0 {
		i.runtime.NextEventJSON()
	}
	n := uint32(i.runtime.EventJSONLength())
	b, ok := region(out, 4)
	if !ok {
		return 4
	}
	put32(b, 0, n)
	if n == 0 {
		return 0
	}
	if size < n {
		return 3
	}
	b, ok = region(dst, n)
	if !ok || !i.runtime.EventJSONCopy(b) {
		return 4
	}
	return 0
}
