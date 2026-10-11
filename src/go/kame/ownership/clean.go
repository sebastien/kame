package ownership

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Decision struct {
	Path     string
	Producer string
	State    string
	Reason   string
	Recovery string
}

func FreeDecisions(a mem.Allocator, items []Decision) {
	for i := range items {
		mem.FreeString(a, items[i].Path)
		mem.FreeString(a, items[i].Producer)
		mem.FreeString(a, items[i].Recovery)
	}
	slices.Free(a, items)
}

func equal(a []byte, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) marker(name string) int {
	data := s.read(name)
	slices.Free(s.Alloc, data)
	return s.Status
}

func (s *Store) Protect(name string, release bool) bool {
	if s.cancelled() {
		s.Status = -4
		return false
	}
	member := s.recordPath(name)
	defer mem.FreeString(s.Alloc, member)
	if member == "" {
		s.Status = -3
		return false
	}
	if !s.Open(true) {
		return false
	}
	id := key(s.Alloc, member)
	defer mem.FreeString(s.Alloc, id)
	if release {
		// An interrupted quarantine retains its record and receipt until a user
		// has recovered the bytes. Release must not silently abandon recovery.
		if s.marker(id+".recovery") != 1 {
			s.Status = -3
			return false
		}
		s.Status = int(km_own_remove(s.native, id+".record"))
	} else {
		s.Status = int(km_own_write(s.native, id+".keep", []byte(member)))
	}
	return s.Status == 0
}

func (s *Store) Clean(context [32]byte, verified bool, dry bool, protected []string) []Decision {
	var items []Decision
	if s.cancelled() {
		return slices.Append(s.Alloc, items, Decision{State: "error", Reason: "cleanup-cancelled"})
	}
	if !s.Open(false) {
		if s.Status != 1 {
			items = slices.Append(s.Alloc, items, Decision{State: "error", Reason: statusReason(s.Status)})
		}
		return items
	}
	size := int(km_own_list(s.native, nil))
	if size < 0 {
		return slices.Append(s.Alloc, items, Decision{State: "error", Reason: "ledger-unreadable"})
	}
	data := slices.Make[byte](s.Alloc, size)
	if int(km_own_list(s.native, data)) != size {
		slices.Free(s.Alloc, data)
		return slices.Append(s.Alloc, items, Decision{State: "error", Reason: "ledger-unreadable"})
	}
	start := 0
	for i := range data {
		if data[i] != 0 {
			continue
		}
		if s.cancelled() {
			items = slices.Append(s.Alloc, items, Decision{State: "error", Reason: "cleanup-cancelled"})
			break
		}
		name := string(data[start:i])
		start = i + 1
		items = slices.Append(s.Alloc, items, s.cleanMember(name, context, verified, dry, protected))
	}
	slices.Free(s.Alloc, data)
	if s.cancelled() {
		found := false
		for i := range items {
			if items[i].Reason == "cleanup-cancelled" {
				found = true
			}
		}
		if !found {
			items = slices.Append(s.Alloc, items, Decision{State: "error", Reason: "cleanup-cancelled"})
		}
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Path < items[j-1].Path; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return items
}

func statusReason(status int) string {
	if status == -4 {
		return "cleanup-cancelled"
	}
	if status == -2 {
		return "project-busy"
	}
	if status == -3 {
		return "unsafe-or-unsupported-host-operation"
	}
	return "filesystem-error"
}

func (s *Store) cleanMember(name string, context [32]byte, verified bool, dry bool, protected []string) Decision {
	result := Decision{State: "error", Reason: "ledger-invalid"}
	data := s.read(name)
	if s.Status != 0 {
		slices.Free(s.Alloc, data)
		return result
	}
	r := s.decode(data)
	slices.Free(s.Alloc, data)
	defer r.free(s.Alloc)
	if s.Status != 0 {
		return result
	}
	id := key(s.Alloc, r.Path)
	defer mem.FreeString(s.Alloc, id)
	if name != id+".record" || !equal(r.Root[:], s.ID[:]) {
		return result
	}
	result.Path, result.Producer = clone(s.Alloc, r.Path), clone(s.Alloc, r.Producer)
	result.State, result.Reason = "preserved", "uncertain-production"
	if !safeMember(r.Path) {
		result.Reason = "outside-cleanup-scope"
		return result
	}
	if s.marker(id+".recovery") != 1 {
		result.State, result.Reason = "error", "recovery-required"
		result.Recovery = clone(s.Alloc, ".kame/outputs/"+id+".artifact")
		return result
	}
	if !r.Accepted {
		return result
	}
	marker := s.marker(r.Batch + ".batch")
	if marker == 0 {
		return result
	}
	if marker != 1 {
		result.State, result.Reason = "error", "ledger-invalid"
		return result
	}
	marker = s.marker(id + ".keep")
	if marker == 0 {
		result.Reason = "explicitly-protected"
		return result
	}
	if marker != 1 {
		result.State, result.Reason = "error", "ledger-invalid"
		return result
	}
	for i := range protected {
		if protected[i] == r.Path {
			result.Reason = "current-source-or-external-input"
			return result
		}
	}
	current := s.observe(r.Path, true)
	if current.Status == 1 {
		result.State, result.Reason = "missing", "already-missing"
		if !dry && km_own_remove(s.native, name) != 0 {
			result.State, result.Reason = "error", "ledger-update-failed"
		}
		return result
	}
	if current.Status < 0 {
		result.State, result.Reason = "error", statusReason(current.Status)
		return result
	}
	if current.Status != 0 || !equal(current.Stamp[:], r.Stamp[:]) || !equal(current.Digest[:], r.Digest[:]) {
		result.Reason = "artifact-changed"
		return result
	}
	if !verified {
		result.Reason = "context-unverifiable"
		return result
	}
	if !equal(context[:], r.Context[:]) {
		result.Reason = "build-context-changed"
		return result
	}
	if dry {
		result.State, result.Reason = "would-remove", "unchanged-owned-artifact"
		return result
	}
	if s.cancelled() {
		result.State, result.Reason = "error", "cleanup-cancelled"
		return result
	}
	// Write the receipt BEFORE capturing the public member. Any interruption
	// leaves either its original bytes or an explicitly discoverable recovery copy.
	if km_own_write(s.native, id+".recovery", []byte(r.Path)) != 0 {
		result.State, result.Reason = "error", "ledger-update-failed"
		return result
	}
	captured := int(km_own_capture(s.native, r.Path, id+".artifact", r.Stamp[:]))
	if captured == 1 || captured == 2 {
		if km_own_remove(s.native, id+".recovery") != 0 {
			result.State, result.Reason = "error", "ledger-update-failed"
			return result
		}
		result.Reason = "artifact-changed"
		return result
	}
	if captured != 0 {
		result.State, result.Reason = "error", "recovery-required"
		result.Recovery = clone(s.Alloc, ".kame/outputs/"+id+".artifact")
		return result
	}
	moved := s.observe(id+".artifact", false)
	// Rename updates ctime. Compare object identity, mode, size, mtime and bytes;
	// the descriptor observation also rejects changes while hashing the object.
	if moved.Status != 0 || !equal(moved.Stamp[:40], r.Stamp[:40]) || !equal(moved.Digest[:], r.Digest[:]) || s.cancelled() {
		if km_own_restore(s.native, r.Path, id+".artifact") != 0 {
			result.State, result.Reason = "error", "recovery-required"
			result.Recovery = clone(s.Alloc, ".kame/outputs/"+id+".artifact")
			return result
		}
		if km_own_remove(s.native, id+".recovery") != 0 {
			result.State, result.Reason = "error", "ledger-update-failed"
			return result
		}
		result.Reason = "artifact-changed"
		return result
	}
	if km_own_remove(s.native, id+".artifact") != 0 {
		result.State, result.Reason = "error", "recovery-required"
		result.Recovery = clone(s.Alloc, ".kame/outputs/"+id+".artifact")
		return result
	}
	// Captured bytes are gone: clear the receipt before retiring the record so
	// interruption leaves a retryable missing/changed entry, not an orphan receipt
	// that invisibly blocks future production. A replacement still has to match
	// the old object identity, complete metadata and bytes before deletion.
	if km_own_remove(s.native, id+".recovery") != 0 || km_own_remove(s.native, name) != 0 {
		result.State, result.Reason = "error", "ledger-update-failed"
		return result
	}
	result.State, result.Reason = "removed", "unchanged-owned-artifact"
	return result
}
