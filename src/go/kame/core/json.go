package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

// ParseJSON parses one canonical JSON document into an owned
// Value. It is the structured half of host completion: hosts return
// records and lists as canonical JSON and the engine owns the parsed value.
// It reports false on any syntax or shape error, leaving out untouched.
func ParseJSON(a mem.Allocator, data []byte, out *Value) bool {
	p := jsonParser{alloc: a, data: data}
	var value Value
	ok := p.value(&value)
	p.whitespace()
	if !ok || p.pos != len(p.data) {
		value.Free(a)
		return false
	}
	*out = value
	return true
}

type jsonParser struct {
	alloc mem.Allocator
	data  []byte
	pos   int
	depth int
}

func (p *jsonParser) whitespace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) value(out *Value) bool {
    if p.depth >= 256 { return false }
    p.depth++
    valid := p.innerValue(out)
    p.depth--
    return valid
}

func (p *jsonParser) innerValue(out *Value) bool {
	p.whitespace()
	if p.pos >= len(p.data) {
		return false
	}
	switch p.data[p.pos] {
	case 'n':
		if p.literal("null") {
			*out = Value{Kind: Nil}
			return true
		}
	case 't':
		if p.literal("true") {
			*out = Value{Kind: Bool, Bool: true}
			return true
		}
	case 'f':
		if p.literal("false") {
			*out = Value{Kind: Bool, Bool: false}
			return true
		}
	case '"':
		text, ok := p.string()
		if ok {
			*out = Value{Kind: String, Text: text}
			return true
		}
	case '[':
		return p.array(out)
	case '{':
		return p.object(out)
	default:
		return p.number(out)
	}
	return false
}

func (p *jsonParser) literal(word string) bool {
	if p.pos+len(word) > len(p.data) {
		return false
	}
	if string(p.data[p.pos:p.pos+len(word)]) != word {
		return false
	}
	p.pos += len(word)
	return true
}

func (p *jsonParser) number(out *Value) bool {
	start := p.pos
	if p.pos < len(p.data) && p.data[p.pos] == '-' {
		p.pos++
	}
	digits := 0
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		p.pos++
		digits++
	}
	numberStart := start
	if p.data[start] == '-' { numberStart++ }
	if digits > 1 && p.data[numberStart] == '0' { return false }
	if digits == 0 {
		return false
	}
	isFloat := false
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		isFloat = true
		p.pos++
		frac := 0
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
			frac++
		}
		if frac == 0 {
			return false
		}
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		isFloat = true
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		exponent := 0
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
			exponent++
		}
		if exponent == 0 {
			return false
		}
	}
	text := string(p.data[start:p.pos])
	if !isFloat {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			*out = Value{Kind: Int, Int: n}
			return true
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return false
	}
	*out = Value{Kind: Float, Float: f}
	return true
}

// string returns an allocator-owned string with escapes resolved. It reports
// false on malformed input, freeing any partial buffer it built.
func (p *jsonParser) string() (string, bool) {
	if p.pos >= len(p.data) || p.data[p.pos] != '"' {
		return "", false
	}
	p.pos++
	var out []byte
	start := p.pos
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		switch {
		case c == '"':
			p.pos++
			if out == nil {
				return NewString(p.alloc, string(p.data[start:p.pos-1])).Text, true
			}
			out = appendBytes(p.alloc, out, p.data, start, p.pos-1)
			text := NewString(p.alloc, string(out)).Text
			slices.Free(p.alloc, out)
			return text, true
		case c == '\\':
			out = appendBytes(p.alloc, out, p.data, start, p.pos)
			p.pos++
			if !p.escape(&out) {
				slices.Free(p.alloc, out)
				return "", false
			}
			start = p.pos
		case c < 0x20:
			if out != nil {
				slices.Free(p.alloc, out)
			}
			return "", false
		default:
			p.pos++
		}
	}
	if out != nil {
		slices.Free(p.alloc, out)
	}
	return "", false
}

func (p *jsonParser) escape(out *[]byte) bool {
	if p.pos >= len(p.data) {
		return false
	}
	c := p.data[p.pos]
	p.pos++
	switch c {
	case '"', '\\', '/':
		*out = slices.Append(p.alloc, *out, c)
	case 'b':
		*out = slices.Append(p.alloc, *out, '\b')
	case 'f':
		*out = slices.Append(p.alloc, *out, '\f')
	case 'n':
		*out = slices.Append(p.alloc, *out, '\n')
	case 'r':
		*out = slices.Append(p.alloc, *out, '\r')
	case 't':
		*out = slices.Append(p.alloc, *out, '\t')
	case 'u':
		r, ok := p.hex4()
		if !ok { return false }
        if r >= 0xD800 && r <= 0xDBFF {
            if p.pos+2 > len(p.data) || p.data[p.pos] != '\\' || p.data[p.pos+1] != 'u' { return false }
            p.pos += 2
            low, valid := p.hex4()
            if !valid || low < 0xDC00 || low > 0xDFFF { return false }
            r = 0x10000 + (r-0xD800)*0x400 + low-0xDC00
        } else if r >= 0xDC00 && r <= 0xDFFF { return false }
		*out = appendRune(p.alloc, *out, r)
	default:
		return false
	}
	return true
}

func (p *jsonParser) hex4() (int, bool) {
	if p.pos+4 > len(p.data) {
		return 0, false
	}
	value := 0
	for i := 0; i < 4; i++ {
		c := p.data[p.pos+i]
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value |= int(c - '0')
		case c >= 'a' && c <= 'f':
			value |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			value |= int(c-'A') + 10
		default:
			return 0, false
		}
	}
	p.pos += 4
	return value, true
}

func appendBytes(a mem.Allocator, out []byte, data []byte, start int, end int) []byte {
	for i := start; i < end; i++ {
		out = slices.Append(a, out, data[i])
	}
	return out
}

func appendRune(a mem.Allocator, out []byte, r int) []byte {
	switch {
	case r < 0x80:
		return slices.Append(a, out, byte(r))
	case r < 0x800:
		return slices.Append(a, out, byte(0xC0|(r>>6)), byte(0x80|(r&0x3F)))
	case r < 0x10000:
		return slices.Append(a, out, byte(0xE0|(r>>12)), byte(0x80|((r>>6)&0x3F)), byte(0x80|(r&0x3F)))
	default:
		return slices.Append(a, out, byte(0xF0|(r>>18)), byte(0x80|((r>>12)&0x3F)), byte(0x80|((r>>6)&0x3F)), byte(0x80|(r&0x3F)))
	}
}

func (p *jsonParser) array(out *Value) bool {
	p.pos++ // consume '['
	var values []Value
	p.whitespace()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		*out = NewList(p.alloc, values)
		return true
	}
	for {
		var child Value
		if !p.value(&child) {
			break
		}
		values = slices.Append(p.alloc, values, child)
		p.whitespace()
		if p.pos >= len(p.data) {
			break
		}
		if p.data[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.data[p.pos] == ']' {
			p.pos++
			*out = NewList(p.alloc, values)
			freeValues(p.alloc, values)
			slices.Free(p.alloc, values)
			return true
		}
		break
	}
	freeValues(p.alloc, values)
	slices.Free(p.alloc, values)
	return false
}

func (p *jsonParser) object(out *Value) bool {
	p.pos++ // consume '{'
	var fields []RecordField
	p.whitespace()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		*out = NewRecord(p.alloc, fields)
		return true
	}
	for {
		p.whitespace()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			break
		}
		key, ok := p.string()
		if !ok {
			break
		}
		p.whitespace()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			mem.FreeString(p.alloc, key)
			break
		}
		p.pos++
		var value Value
		if !p.value(&value) {
			mem.FreeString(p.alloc, key)
			break
		}
		fields = slices.Append(p.alloc, fields, RecordField{Key: key, Value: value})
		p.whitespace()
		if p.pos < len(p.data) && p.data[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.pos < len(p.data) && p.data[p.pos] == '}' {
			p.pos++
			*out = NewRecord(p.alloc, fields)
			freeFields(p.alloc, fields)
			slices.Free(p.alloc, fields)
			return true
		}
		break
	}
	freeFields(p.alloc, fields)
	slices.Free(p.alloc, fields)
	return false
}

func freeValues(a mem.Allocator, values []Value) {
	for i := range values {
		values[i].Free(a)
	}
}

func freeFields(a mem.Allocator, fields []RecordField) {
	for i := range fields {
		mem.FreeString(a, fields[i].Key)
		fields[i].Value.Free(a)
	}
}
