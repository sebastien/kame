package scriptcheck

import (
	"kame/internal/testing"
	"kame/lang/script"
)

type kashInputCase struct {
	Text string
	Want script.KashInput
}

func TestKashInteractiveInputClassification(t *testing.T) {
	cases := []kashInputCase{
		{"", script.KashComplete},
		{"echo hello", script.KashComplete},
		{"let x = (uppercase \"a\")", script.KashComplete},
		{"echo (map", script.KashOpen},
		{"let x = (map", script.KashOpen},
		{"echo \"open", script.KashOpen},
		{"echo (map\n  uppercase [\"a\"])", script.KashComplete},
		{"def greet name", script.KashBlock},
		{"if test -e ./file", script.KashBlock},
		{"for name in [\"a\"]", script.KashBlock},
		{"def greet name\n\techo $name", script.KashComplete},
		{"def greet name\n\tif true", script.KashBlock},
		{"echo )", script.KashComplete},
		{"alias ll = [", script.KashOpen},
		{"alias ll = [\"ls\"\n \"-lah\"]", script.KashComplete},
		{"alias ll = [\"open", script.KashOpen},
		{"alias ll = []", script.KashComplete},
	}
	for i := range cases {
		if got := script.ClassifyKashInput(cases[i].Text); got != cases[i].Want {
			t.Error("unexpected interactive input class: " + cases[i].Text)
		}
	}
}
