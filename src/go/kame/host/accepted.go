package host

import "kame/core"

func ValidAcceptedExits(value core.Value) bool {
	if value.Kind != core.List {
		return false
	}
	var seen [256]bool
	for i := range value.List {
		v := value.List[i]
		if v.Kind != core.Int || v.Int < 1 || v.Int > 255 || seen[v.Int] {
			return false
		}
		seen[v.Int] = true
	}
	return true
}
