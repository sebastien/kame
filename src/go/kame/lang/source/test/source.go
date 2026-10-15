package source_test

import (
	"kame/lang/source"
	"solod.dev/so/testing"
)

func TestPositionCountsCRLFTabsAndUTF8(t *testing.T) {
	s := source.New(t.Allocator(), "test.km", "one\r\n\t\xc3\xa9x")
	position := s.Position(len(s.Text))
	if position.Line != 2 || position.Column != 11 {
		t.Errorf("Position() = %d:%d, want 2:11", position.Line, position.Column)
	}
	s.Free(t.Allocator())
}

type topLevelCase struct {
	Text  string
	Want  byte
	Index int
}

func TestTopLevelByte(t *testing.T) {
	cases := []topLevelCase{
		{"", '=', -1},
		{"plain", '=', -1},
		{"=first=last", '=', 0},
		{"(=)[=]{=}=", '=', 9},
		{"({[=]})=", '=', 7},
		{"(=", '=', -1},
		{"([)=]", '=', -1},
		{")=", '=', -1},
		{")=(=", '=', 3},
		{")](==", '=', -1},
		{"([)]=", '=', 4},
		{`"="=`, '=', 3},
		{`"\"="=`, '=', 5},
		{`"\\"=`, '=', 4},
		{`"\x="=`, '=', 5},
		{`"=\`, '=', -1},
		{`'='=`, '=', 1},
		{`\=`, '=', 1},
		{"\xc3\xa9=", '=', 2},
		{"[;];", ';', 3},
		{`":" :`, ':', 4},
		{"()", '(', -1},
		{"()", ')', -1},
		{`""`, '"', -1},
	}
	for i := range cases {
		c := cases[i]
		if got := source.TopLevelByte(c.Text, c.Want); got != c.Index {
			t.Errorf("TopLevelByte(%q, %d) = %d, want %d", c.Text, c.Want, got, c.Index)
		}
	}
}
