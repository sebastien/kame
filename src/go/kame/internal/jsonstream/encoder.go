// Package jsonstream writes JSON values incrementally to an io.Writer.
package jsonstream

import (
	"bytes"
	"encoding/json"
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
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(s)
	b := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	if err != nil {
		e.err = err
	}
	e.write(string(b))
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
