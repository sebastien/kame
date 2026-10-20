package wasm

// BeginFinalization is explicitly pumped by hosts before freeing an instance.
// Complete/reap outstanding requests first; then service cleanup requests under
// the original grants but a fresh, bounded cancellation/deadline context.
func (r *Runtime) BeginFinalization() bool {
	if r == nil {
		return false
	}
	if r.Finalizing {
		return true
	}
	r.Cancel()
	r.Finalizing = true
	if p := r.finalizationProgram(); p != nil {
		p.BeginFinalization()
	}
	return true
}
