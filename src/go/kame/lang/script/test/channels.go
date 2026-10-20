package script_test

import (
	"kame/lang/expr"
	"kame/lang/format"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestChannelDeclarationsAreLexicalSyntax(t *testing.T) {
	s := script.ParseKash(t.Allocator(), "channels.kash", "stream reports\n(printf a >&reports, &reports | cat)\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 {
		t.Error("named channel group did not parse")
		return
	}
	if len(s.Items) != 2 || s.Items[0].Expression.Kind != expr.CommandStream {
		t.Error("stream declaration became an executable")
		return
	}
	e := s.Items[1].Expression
	if len(e.Channels) != 1 || e.Channels[0] != "reports" {
		t.Error("graph lost its lexical declarations")
	}
	clone := expr.Clone(t.Allocator(), e)
	expr.Free(t.Allocator(), clone)
	formatted := format.Source(t.Allocator(), "kash", "channels.kash", s.Source.Text, "tabs", 4)
	if !formatted.OK {
		t.Error("channel script failed formatting")
	} else {
		copy := script.ParseKash(t.Allocator(), "formatted.kash", formatted.Text)
		if len(copy.Diagnostics) != 0 || len(copy.Items) != 2 {
			t.Error("formatted channel script did not reparse")
		}
		copy.Free()
	}
	mem.FreeString(t.Allocator(), formatted.Text)
	mem.FreeString(t.Allocator(), formatted.Code)
	mem.FreeString(t.Allocator(), formatted.Message)
}

func TestMalformedChannelDeclarationsFailParsing(t *testing.T) {
	texts := []string{"stream", "stream 123", "stream in", "stream reports extra", "stream reports\nstream reports"}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Errorf("invalid stream declaration accepted: %s", texts[i])
		}
		s.Free()
	}
}
