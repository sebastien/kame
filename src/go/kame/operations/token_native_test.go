package operations

import (
	"testing"
	"unicode"

	sounicode "solod.dev/so/unicode"
)

// Check the supplemental numeric ranges against the versioned standard table,
// rather than maintaining an independent hand-written list of sample numbers.
func TestTokenUnicodeTable(t *testing.T) {
	if unicode.Version != "15.0.0" || sounicode.Version != unicode.Version {
		t.Fatal("update token tables and operation version together when upgrading Unicode")
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		want := r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsNumber(r)
		if tokenCharacter(r) != want {
			t.Fatalf("token classification differs at U+%04X", r)
		}
	}
}
