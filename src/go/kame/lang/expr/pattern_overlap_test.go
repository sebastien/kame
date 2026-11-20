package expr

import (
	"kame/internal/mem"
	"testing"
)

func TestPathPatternsOverlap(t *testing.T) {
	a := mem.System
	for _, tc := range []struct {
		scan, discovery, output string
		want                    bool
	}{
		{scan: "/src/*.c", output: "/src/{name:*}.c", want: true},
		{scan: "/src/*.c", output: "/src/{name:*}.h"},
		{scan: "/src/**/mod.c", output: "/src/{name:*}.c", want: true},
		{scan: "/src/**/mod.c", output: "/src/{name:*}/two/mod.c", want: true},
		{scan: "/src/*mod.c", output: "/src/mod{suffix:~\\.c}", want: true},
		{scan: "/src/?.txt", output: "/src/{name:é}.txt", want: true},
		{scan: "/src/?.txt", output: "/src/{name:ab}.txt"},
		{scan: "/src/[é-ê].txt", output: "/src/{name:ê}.txt", want: true},
		{scan: "/src/[^é-ê].txt", output: "/src/{name:é}.txt"},
		{scan: "/src/[^é-ê].txt", output: "/src/{name:ë}.txt", want: true},
		{scan: "/src/\\[literal\\]*.c", output: "/src/\\[literal\\]{name:*}.c", want: true},
		{discovery: "/src/{name:~[a-z]+}.c", output: "/src/{name:~[a-z][a-z]}.c", want: true},
		{discovery: "/src/{name:~[a-z]+}.c", output: "/src/{name:~[0-9]+}.c"},
		{discovery: "/src/{name:~[a-z]+/[0-9]+}.c", output: "/src/{first:*}/{last:*}.c", want: true},
		{scan: "/src/.c", output: "/src/{name:~a*}.c"},
		{discovery: "/src/{name:[ab]}-{name:[ab]}.c", output: "/src/{name:a}-{last:b}.c"},
		{discovery: "/src/{name:[ab]}-{name:[ab]}.c", output: "/src/{name:a}-{last:a}.c", want: true},
		{discovery: "/src/{name:*}-{name:*}.c", output: "/src/{name:~[0-9]+}-match.c"},
		{discovery: "/src/{name:*}-{name:*}.c", output: "/src/{name:~[0-9]+}-12345.c", want: true},
		{discovery: "/src/{name:*}-{name:*}.c", output: "/src/{name:*}-absent.c", want: true},
		{discovery: "/src/{x:[ab]}{y:[ab]}-{y:[ab]}{x:[ab]}.c", output: "/src/{start:ab}-{end:ba}.c", want: true},
		{discovery: "/src/{x:[ab]}{y:[ab]}-{y:[ab]}{x:[ab]}.c", output: "/src/{start:ab}-{end:ab}.c"},
		{scan: "/src/**/mod.c", output: "/src/{dot:.}/mod.c"},
		{scan: "/src/**", output: "/src/{dot:..}"},
		{scan: "../src/*.c", output: "../src/{name:*}.c", want: true},
	} {
		t.Run(tc.scan+tc.discovery+" vs "+tc.output, func(t *testing.T) {
			output := ParsePathPatternText(a, tc.output)
			defer output.Pattern.Free(a)
			if len(output.Diagnostics) != 0 {
				t.Fatalf("invalid output fixture: %v", output.Diagnostics)
			}
			var discovery *Pattern
			if tc.discovery != "" {
				parsed := ParsePathPatternText(a, tc.discovery)
				if len(parsed.Diagnostics) != 0 {
					t.Fatalf("invalid discovery fixture: %v", parsed.Diagnostics)
				}
				discovery = parsed.Pattern
				defer discovery.Free(a)
			}
			budget := RegexStepLimit
			matched, limited := PathPatternsOverlap(a, discovery, output.Pattern, tc.scan, &budget)
			if limited || matched != tc.want {
				t.Fatalf("matched %t, limited %t, want %t", matched, limited, tc.want)
			}
		})
	}
}
