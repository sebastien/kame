package expr

import "solod.dev/so/slices"

// Policies are closed syntax, never evaluated configuration. In particular,
// inspection and invalid graphs must not execute arbitrary option expressions.
func (p *parser) streamPolicy(mux *Expr) {
	buffer, strategy := false, false
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] != ')' && len(p.diags) == 0 {
		start := p.pos
		key := p.expression()
		if key != nil { mux.Items = slices.Append(p.a, mux.Items, key) }
		p.commandSpace(true)
		if key == nil || key.Kind != Symbol || (key.Text != "buffer" && key.Text != "strategy") || p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' { p.error(start, p.pos, "expected :buffer BYTES or :strategy round-robin"); break }
		value := p.expression()
		if value != nil { mux.Items = slices.Append(p.a, mux.Items, value) }
		if key.Text == "buffer" {
			if buffer || value == nil || value.Kind != Integer || value.Int < 1 || value.Int > 65536 { p.error(start, p.pos, ":buffer requires one literal byte budget in 1..65536") } else { mux.Int, buffer = value.Int, true }
		} else {
			if strategy || mux.Text != "balance" || value == nil || value.Kind != Name || value.Text != "round-robin" { p.error(start, p.pos, "only balance :strategy round-robin is supported") }
			strategy = true
		}
		p.commandSpace(true)
	}
}
