package expr

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func ChannelName(text string) bool {
	if text == "" {
		return false
	}
	for i := range text {
		if !channelNameByte(text[i], i == 0) {
			return false
		}
	}
	return text != "in" && text != "out" && text != "err"
}

func channelNameByte(b byte, first bool) bool { return setupKeyByte(b, first) || (!first && b == '-') }

func (p *parser) channelEndpoint(kind Kind, prefix int) *Expr {
	endpoint := p.node(kind, p.pos)
	p.pos += prefix
	start := p.pos
	for p.pos < len(p.s.Text) && channelNameByte(p.s.Text[p.pos], p.pos == start) {
		p.pos++
	}
	endpoint.Text = p.s.Text[start:p.pos]
	endpoint.Span.End = p.pos
	reservedOutput := kind == CommandWriter && (endpoint.Text == "out" || endpoint.Text == "err")
	if !reservedOutput && !ChannelName(endpoint.Text) {
		p.error(start, p.pos, "invalid or reserved channel name")
	}
	return endpoint
}

// Declarations are lexical syntax, not Kame values or live channel objects.
// Each separately owned graph must bind both endpoints itself.
func SetCommandChannels(a mem.Allocator, e *Expr, names []string) {
	if e == nil {
		return
	}
	if e.Kind == CommandGraph || e.Kind == CommandCapture || e.Kind == CommandTest {
		for i := range names {
			if !slices.Contains(e.Channels, names[i]) {
				e.Channels = slices.Append(a, e.Channels, sourceText(a, names[i]))
			}
		}
	}
	for i := range e.Items {
		SetCommandChannels(a, e.Items[i], names)
	}
	for i := range e.Body {
		SetCommandChannels(a, e.Body[i], names)
	}
	for i := range e.Fields {
		SetCommandChannels(a, e.Fields[i].Value, names)
	}
	for i := range e.Parts {
		SetCommandChannels(a, e.Parts[i].Expr, names)
	}
}
