package program

// A cancelled engine request can still own a live child. Finalizers wait for
// terminal delivery (native) or host completion (forwarded), not just cancellation.
func deferredProcessesQuiet(state any, owner int64, group int64) bool {
	p := state.(*Program)
	for i := range p.Pending {
		node := p.Pending[i].NodeID
		if (owner == 0 && group == 0) || node == owner || p.Eval.ProcessInGroup(node, group) {
			return false
		}
	}
	return true
}
