// Package jsonstream writes JSON values incrementally to an io.Writer.
package jsonstream

import (
	"errors"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

type frame struct {
	object bool
	count  int
}
type Encoder struct {
	out   io.Writer
	stack []frame
	err   error
}

func NewEncoder(w io.Writer) Encoder { return Encoder{out: w} }
func (e *Encoder) write(s string) {
	if e.err == nil {
		_, e.err = io.WriteString(e.out, s)
	}
}
func (e *Encoder) sep() {
	if len(e.stack) == 0 {
		return
	}
	f := &e.stack[len(e.stack)-1]
	if f.object && f.count%2 == 1 {
		e.write(":")
	} else if f.count > 0 {
		e.write(",")
	}
	f.count++
}
func (e *Encoder) BeginObject() {
	if len(e.stack) >= 256 {
		e.err = errors.New("JSON depth limit exceeded")
	}
	e.sep()
	e.write("{")
	e.stack = append(e.stack, frame{object: true})
}
func (e *Encoder) EndObject() { e.write("}"); e.stack = e.stack[:len(e.stack)-1] }
func (e *Encoder) BeginArray() {
	if len(e.stack) >= 256 {
		e.err = errors.New("JSON depth limit exceeded")
	}
	e.sep()
	e.write("[")
	e.stack = append(e.stack, frame{})
}
func (e *Encoder) EndArray() { e.write("]"); e.stack = e.stack[:len(e.stack)-1] }
func (e *Encoder) Str(s string) {
	if !utf8.ValidString(s) {
		e.err = errors.New("invalid UTF-8 in JSON string")
		return
	}
	e.sep()
	if e.err == nil {
		_, e.err = e.out.Write(AppendString(make([]byte, 0, len(s)+2), s))
	}
}

const hexDigits = "0123456789abcdef"

// AppendString appends s as a JSON string, byte-identical to encoding/json
// with HTML escaping disabled: short escapes for \b \f \n \r \t, \u00XX for
// other control bytes, and escaped U+2028/U+2029. s must be valid UTF-8.
func AppendString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' && c < utf8.RuneSelf {
			i++
			continue
		}
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r != '\u2028' && r != '\u2029' {
				i += size
				continue
			}
			b = append(append(b, s[start:i]...), '\\', 'u', '2', '0', '2', hexDigits[r&0xf])
			i += size
			start = i
			continue
		}
		b = append(b, s[start:i]...)
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		i++
		start = i
	}
	return append(append(b, s[start:]...), '"')
}

func (e *Encoder) Int(n int64) { e.sep(); e.write(strconv.FormatInt(n, 10)) }
func (e *Encoder) Float(n float64) {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		e.err = errors.New("invalid JSON number")
		return
	}
	e.sep()
	e.write(strconv.FormatFloat(n, 'g', -1, 64))
}
func (e *Encoder) Bool(v bool) { e.sep(); e.write(strconv.FormatBool(v)) }
func (e *Encoder) Null()       { e.sep(); e.write("null") }
func (e *Encoder) Flush()      {}
func (e *Encoder) Err() error  { return e.err }
