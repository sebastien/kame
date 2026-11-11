package main

import (
	"kame/host/wasm"
)

func query(i *instance, r wasm.PureResult, dst, size, out uint32) uint32 {
	if r.HostNeeded {
		return 6
	}
	return finish(r, dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_tools
func query_tools(handle uint64, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending != nil {
		return 6
	}
	return query(i, i.runtime.ToolNames(), dst, size, out)
}

//go:wasmexport kame_wasm_work_prepare
func query_work_prepare(handle uint64, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending != nil {
		b, ok := region(out, 4)
		if !ok {
			return 4
		}
		put32(b, 0, 0)
		return 0
	}
	return query(i, i.runtime.PrepareWorkJSON(), dst, size, out)
}

//go:wasmexport kame_wasm_watch_state
func query_watch_state(handle uint64, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	return query(i, i.runtime.WatchStateJSON(), dst, size, out)
}

//go:wasmexport kame_wasm_result_diagnostic
func query_result_diagnostic(handle uint64, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if i.runtime == nil {
		b, ok := region(out, 4)
		if !ok {
			return 4
		}
		put32(b, 0, 0)
		return 0
	}
	return query(i, i.runtime.ResultDiagnosticJSON(), dst, size, out)
}

//go:wasmexport kame_wasm_target_operands
func query_target_operands(handle uint64, src, length, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	if i.pending != nil {
		return 6
	}
	return query(i, i.runtime.TargetOperandsJSON(s), dst, size, out)
}

//go:wasmexport kame_wasm_tools_check
func query_tools_check(handle uint64, src, length, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	if i.pending != nil {
		return 6
	}
	return query(i, i.runtime.ToolsCheckJSON(s), dst, size, out)
}

//go:wasmexport kame_wasm_tools_check_report
func query_tools_check_report(handle uint64, src, length, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	if i.pending != nil {
		return 6
	}
	return query(i, i.runtime.ToolsCheckReportJSON(s), dst, size, out)
}

//go:wasmexport kame_wasm_set_build_sources
func set_set_build_sources(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return i.status(i.runtime.SetBuildSources(s))
}

//go:wasmexport kame_wasm_register_plugins
func set_register_plugins(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return i.status(i.runtime.RegisterPluginsJSON(s))
}

//go:wasmexport kame_wasm_session_compile
func set_session_compile(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return i.status(i.runtime.PrepareSession(s))
}

//go:wasmexport kame_wasm_watch_begin
func set_watch_begin(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return i.status(i.runtime.RequestWatch(s))
}

//go:wasmexport kame_wasm_watch_invalidate
func set_watch_invalidate(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return i.status(i.runtime.InvalidateWatch(s))
}

//go:wasmexport kame_wasm_prepare
func prepare(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	return i.status(i.runtime.Prepare())
}

//go:wasmexport kame_wasm_plan
func plan(handle uint64, src, length, expand, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	if i.pending != nil {
		return 6
	}
	return query(i, i.runtime.PlanJSON(s, expand != 0), dst, size, out)
}

//go:wasmexport kame_wasm_graph
func graph(handle uint64, src, length uint32, depth int32, kind, expand, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok || kind > 5 || depth < -1 {
		return 4
	}
	if i.pending != nil {
		return 6
	}
	var r wasm.PureResult
	switch kind {
	case 5:
		r = i.runtime.ListReportJSON(s)
	case 4:
		r = i.runtime.ListJSON(s)
	case 3:
		r = i.runtime.PlanQueryJSON(s, int(depth), expand != 0)
	case 2:
		r = i.runtime.SpanJSON(s, int(depth), expand != 0)
	default:
		k := "inputs"
		if kind == 1 {
			k = "outputs"
		}
		r = i.runtime.GraphJSON(s, int(depth), k)
	}
	return query(i, r, dst, size, out)
}

//go:wasmexport kame_wasm_set_tool_path
func setToolPath(handle uint64, np, nl, pp, pl uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	n, ok := text(np, nl)
	if !ok {
		return 4
	}
	p, ok := text(pp, pl)
	if !ok || !i.runtime.SetToolPath(n, p) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_inspection_grant
func inspectionGrant(handle uint64, cp, cl, np, nl uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	c, ok := text(cp, cl)
	if !ok {
		return 4
	}
	n, ok := text(np, nl)
	if !ok || !i.runtime.InspectionGrant(c, n) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_watch_cancel
func watchCancel(handle uint64) uint32 { return cancelExpression(handle) }

//go:wasmexport kame_wasm_watch_cancel_message
func watchMessage(handle uint64, src, length, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	return query(i, i.runtime.WatchCancellationMessage(s), dst, size, out)
}

//go:wasmexport kame_wasm_session_work_count
func workCount(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	return uint32(i.runtime.SessionWorkCount())
}

//go:wasmexport kame_wasm_session_exit_status
func sessionExitStatus(handle uint64) int32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return -1
	}
	return int32(i.runtime.SessionExitStatus())
}

//go:wasmexport kame_wasm_capture_limit
func captureLimit(handle uint64) int32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	return int32(i.runtime.CaptureLimit())
}

//go:wasmexport kame_wasm_session_work_kind
func workKind(handle uint64, index uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	return i.runtime.SessionWorkKind(int(index))
}

//go:wasmexport kame_wasm_session_work_begin
func workBegin(handle uint64, index uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending != nil {
		return 4
	}
	return i.status(i.runtime.RequestSessionWork(int(index)))
}

//go:wasmexport kame_wasm_expression_effect_kind
func effectKind(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	return i.runtime.ExpressionEffectKind()
}

//go:wasmexport kame_wasm_expression_effect_length
func effectLength(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	return uint32(i.runtime.ExpressionEffectLength())
}

//go:wasmexport kame_wasm_expression_effect_copy
func effectCopy(handle uint64, dst, size uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	n := uint32(i.runtime.ExpressionEffectLength())
	if n > size {
		return 3
	}
	b, ok := region(dst, n)
	if !ok || !i.runtime.CopyExpressionEffect(b) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_host_set_file
func setFile(handle uint64, pp, pl, dp, dl uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	p, ok := text(pp, pl)
	if !ok {
		return 4
	}
	b, ok := region(dp, dl)
	if !ok || !i.runtime.SetFile(p, b) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_host_set_env
func setEnv(handle uint64, np, nl, vp, vl uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	n, ok := text(np, nl)
	if !ok {
		return 4
	}
	v, ok := text(vp, vl)
	if !ok || !i.runtime.SetEnvironment(n, v) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_finalize_begin
func finalizeBegin(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if !i.runtime.BeginFinalization() {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_finalization_tick
func finalizeTick(handle uint64, now int64, stopping uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	pending := false
	if stopping == 2 {
		pending = i.runtime.FinalizationTimeout(now)
	} else {
		pending = i.runtime.FinalizationTick(now, stopping != 0)
	}
	if pending {
		return 1
	}
	return 0
}

//go:wasmexport kame_wasm_finalization_budget
func finalizeBudget(handle uint64, all uint32) int64 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	node := int64(0)
	if all == 0 && i.pending != nil {
		node = i.pending.value.NodeID
	}
	return i.runtime.FinalizationBudget(node)
}
