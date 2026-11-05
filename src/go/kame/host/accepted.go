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

// EffectiveStatus is separate from the retained raw aggregate. Older hosts
// without selective acceptance keep their ordinary status contract.
func EffectiveStatus(value core.Value) int64 {
	for i := range value.Record {
		if value.Record[i].Key == "effectiveStatus" && value.Record[i].Value.Kind == core.Int {
			return value.Record[i].Value.Int
		}
	}
	return PayloadInt(value, "status")
}

func EffectiveSignal(value core.Value) int64 {
	for i := range value.Record {
		if value.Record[i].Key == "effectiveSignal" && value.Record[i].Value.Kind == core.Int {
			return value.Record[i].Value.Int
		}
	}
	return PayloadInt(value, "signal")
}
