package program

func finalizationNow(state any) int64 {
	p := state.(*Program)
	return p.Host.Monotonic()
}

func finalizationQuiet(state any, node int64, generation int64) bool {
	p := state.(*Program)
	_ = generation // A stale cancelled request must still be reaped.
	for i := range p.Pending { if p.Pending[i].NodeID == node { return false } }
	return true
}
