package expr

import (
	"kame/internal/mem"
	"kame/lang/source"
	"strings"
)

// LiteralCommandWord decodes a single Kash argv word without expanding it.
// The empty string is also accepted as an empty editor prefix.
func LiteralCommandWord(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	p := parser{a: mem.System, s: &source.Source{Text: raw}, kash: true}
	word := p.commandWord(true)
	defer freeExpr(p.a, word)
	defer mem.FreeSlice(nil, p.diags)
	if p.pos != len(raw) || len(p.diags) != 0 || word.CommandHome || word.CommandGlob {
		return "", false
	}
	var text strings.Builder
	for _, part := range word.Parts {
		if part.Expr != nil {
			return "", false
		}
		text.WriteString(part.Text)
	}
	return text.String(), true
}

// CommandHead returns the first argv word of an authored command stage.
func CommandHead(stage *Expr) *Expr {
	if stage.Kind == CommandStage && stage.Kash {
		for _, word := range stage.Items {
			if word.Kind == CommandWord {
				return word
			}
		}
	}
	return nil
}

func InteractiveCommand(name string) bool {
	switch name {
	case "jobs", "fg", "bg", "alias", "unalias", "history", "option", "explain", "complete":
		return true
	}
	return false
}
