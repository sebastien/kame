package expr

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
	case "jobs", "fg", "bg", "alias", "unalias", "history", "option", "explain":
		return true
	}
	return false
}
