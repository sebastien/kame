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
