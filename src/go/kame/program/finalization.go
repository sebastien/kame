package program

// CollectProcesses releases completed async execution state after the host has
// acknowledged reaping. Saved handles retain immutable completion snapshots.
func (p *Program) CollectProcesses() {
	if p.collectingProcesses {
		return
	}
	p.collectingProcesses = true
	defer func() { p.collectingProcesses = false }()
	p.Eval.RetireProcesses(finalizationQuiet, p)
	p.drainCancellations()
}

func finalizationNow(state any) int64 {
	p := state.(*Program)
	return p.Host.Monotonic()
}

func finalizationQuiet(state any, node int64, generation int64) bool {
	p := state.(*Program)
	_ = generation // A stale cancelled request must still be reaped.
	for i := range p.Pending {
		if p.Pending[i].NodeID == node {
			return false
		}
	}
	return true
}
