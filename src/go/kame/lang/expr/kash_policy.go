package expr

import "solod.dev/so/slices"

// Policies are closed syntax, never evaluated configuration. In particular,
// inspection and invalid graphs must not execute arbitrary option expressions.
func (p *parser) streamPolicy(mux *Expr) {
	buffer, strategy, overflow, items, order := false, false, false, false, false
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] != ')' && len(p.diags) == 0 {
		start := p.pos
		key := p.expression()
		if key != nil { mux.Items = slices.Append(p.a, mux.Items, key) }
		p.commandSpace(true)
		if key == nil || key.Kind != Symbol || (key.Text != "buffer" && key.Text != "strategy" && key.Text != "overflow" && key.Text != "items" && key.Text != "order") || p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' { p.error(start, p.pos, "expected a literal stream policy option and value"); break }
		value := p.expression()
		if value != nil { mux.Items = slices.Append(p.a, mux.Items, value) }
		if key.Text == "buffer" {
			if buffer || value == nil || value.Kind != Integer || value.Int < 1 || value.Int > 65536 { p.error(start, p.pos, ":buffer requires one literal byte budget in 1..65536") } else { mux.Int, buffer = value.Int, true }
		} else if key.Text == "strategy" {
			if strategy || mux.Text != "balance" || value == nil || value.Kind != Name || value.Text != "round-robin" { p.error(start, p.pos, "only balance :strategy round-robin is supported") }
			strategy = true
		} else if key.Text == "order" {
			if order || mux.Text != "merge" || value == nil || value.Kind != Name || (value.Text != "ready" && value.Text != "round-robin") { p.error(start, p.pos, "merge :order requires ready or round-robin")
			} else if value.Text == "round-robin" { mux.StreamOrder = 1 }
			order = true
		} else if key.Text == "items" {
			if items || value == nil || value.Kind != Integer || value.Int < 1 || value.Int > 1024 { p.error(start, p.pos, ":items requires one literal pending-record limit in 1..1024") } else { mux.BufferItems, items = int(value.Int), true }
		} else {
			policy := -1
			if value != nil && value.Kind == Name {
				if value.Text == "block" { policy = 0 }
				if value.Text == "drop-new" { policy = 1 }
				if value.Text == "drop-old" { policy = 2 }
				if value.Text == "latest" { policy = 3 }
			}
			if overflow || policy < 0 || (policy != 0 && mux.Text != "broadcast") { p.error(start, p.pos, "lossy overflow is supported only for framed broadcast: drop-new, drop-old, latest") } else { mux.StreamOverflow, overflow = policy, true }
		}
		p.commandSpace(true)
	}
	if mux.StreamOverflow == 0 && items { p.error(mux.Span.Start, p.pos, ":items requires an explicit lossy broadcast overflow policy") }
	if mux.StreamOverflow != 0 && !items { mux.BufferItems = 64; if mux.StreamOverflow == 3 { mux.BufferItems = 1 } }
	if mux.StreamOverflow == 3 && mux.BufferItems != 1 { p.error(mux.Span.Start, p.pos, "latest retains exactly one pending record per destination") }
}

func StreamOverflowName(policy int) string {
	if policy == 1 { return "drop-new" }
	if policy == 2 { return "drop-old" }
	if policy == 3 { return "latest" }
	return "block"
}
