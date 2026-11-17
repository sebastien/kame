package jsonstreamcheck

import (
	"bytes"
	"encoding/json"
	"kame/internal/jsonstream"
	"kame/internal/testing"
	"math/rand"
	"strings"
	"unicode/utf8"
)

func referenceJSONString(s string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.Encode(s)
	return strings.TrimSuffix(buffer.String(), "\n")
}

func TestAppendStringMatchesEncodingJSON(t *testing.T) {
	cases := []string{"", "plain", "<html>&amp;", "  ", "é漢字🙂", "\x7f", "a\"b\\c"}
	for c := 0; c < 0x80; c++ {
		cases = append(cases, "x"+string(rune(c))+"y")
	}
	random := rand.New(rand.NewSource(1))
	runes := []rune{'a', '"', '\\', '\n', 0, 0x1f, 0x7f, 0xe9, 0x2028, 0x2029, 0x1f642, '<', '&'}
	for i := 0; i < 2000; i++ {
		var b strings.Builder
		for n := random.Intn(12); n > 0; n-- {
			b.WriteRune(runes[random.Intn(len(runes))])
		}
		cases = append(cases, b.String())
	}
	for _, s := range cases {
		if !utf8.ValidString(s) {
			continue
		}
		got := string(jsonstream.AppendString(nil, s))
		if want := referenceJSONString(s); got != want {
			t.Errorf("AppendString(%q) = %s, want %s", s, got, want)
			return
		}
		var out bytes.Buffer
		e := jsonstream.NewEncoder(&out)
		e.Str(s)
		if out.String() != got || e.Err() != nil {
			t.Errorf("Encoder.Str(%q) = %s", s, out.String())
			return
		}
	}
}
