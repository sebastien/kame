package format_test

import (
	"kame/lang/format"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestValueFormatterCanonicalizesCompactHeaders(t *testing.T) {
	a := t.Allocator()
	texts := []string{"value=42\n", "(identity x)=x\n", "(f)=42\n", "items+=[1]\n", "value?=42\n", "value??=42\n", "ready!=:true\n", "ready? =:true\n"}
	wants := []string{"value = 42\n", "(identity x) = x\n", "(f) = 42\n", "items += [1]\n", "value ?= 42\n", "value? ?= 42\n", "ready! = :true\n", "ready? = :true\n"}
	for i := range texts {
		result := format.Source(a, "km", "compact.km", texts[i], "tabs", 4)
		if !result.OK || result.Text != wants[i] {
			t.Error("compact definition formatting failed: " + texts[i])
		}
		mem.FreeString(a, result.Text)
		mem.FreeString(a, result.Code)
		mem.FreeString(a, result.Message)
	}
	result := format.Source(a, "km", "invalid.km", "value=42 43", "tabs", 4)
	if result.OK || result.Code != "PARSE_ERR" || result.Message != "expected one definition expression; quote text and bracket lists" || result.Span.Start != 9 || result.Span.End != 11 {
		t.Error("formatter lost compact definition value diagnostic")
	}
	mem.FreeString(a, result.Text)
	mem.FreeString(a, result.Code)
	mem.FreeString(a, result.Message)
}
