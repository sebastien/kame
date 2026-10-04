package core_test

import (
    "kame/core"
    "solod.dev/so/testing"
)

func TestJSONNativeValues(t *testing.T) {
    a := t.Allocator()
    var v core.Value
    if !core.ParseJSON(a, []byte(" \n{\"title\":\"A\\nB\",\"values\":[true,false,null,-42,1.5],\"emoji\":\"\\ud83d\\ude80\",\"private\":\"\\ue000\"}\n "), &v) {
        t.Error("valid JSON rejected")
        return
    }
    if v.Kind != core.Record || len(v.Record) != 4 || v.Record[0].Value.Text != "A\nB" || v.Record[1].Value.List[3].Int != -42 || v.Record[1].Value.List[4].Float != 1.5 || v.Record[2].Value.Text != "🚀" {
        t.Error("JSON native values differ")
    }
    v.Free(a)
}

func TestJSONRejectsMalformedInput(t *testing.T) {
    a := t.Allocator()
    invalid := []string{"", "01", "-01", "1.", "1e", "[1,]", "{\"x\":}", "true false", "\"\\ud800\"", "\"\\udc00\"", "\"\\ud800\\u0041\"", "\"a\n\""}
    for i := range invalid {
        v := core.Value{Kind: core.Int, Int: 7}
        if core.ParseJSON(a, []byte(invalid[i]), &v) { v.Free(a); t.Error("malformed JSON accepted: " + invalid[i]) }
        if v.Kind != core.Int || v.Int != 7 { t.Error("failed parse changed output") }
    }
}

func TestJSONRejectsInvalidUTF8AndExcessiveDepth(t *testing.T) {
    a := t.Allocator()
    var value core.Value
    if core.ParseJSON(a, []byte{'"', 0xff, '"'}, &value) { value.Free(a); t.Error("invalid UTF-8 accepted") }
    var nested [600]byte
    for i := 0; i < 300; i++ { nested[i] = '['; nested[599-i] = ']' }
    if core.ParseJSON(a, nested[:], &value) { value.Free(a); t.Error("excessive nesting accepted") }
}
