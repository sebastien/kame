package main

import (
	"kame/lang/source"
	"solod.dev/so/unicode/utf8"
)

type displayMeasure struct {
	End   int
	Cells int
}

func measureDisplayText(text string, width int) displayMeasure {
	end, cells := 0, 0
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if size == 0 {
			break
		}
		clusterEnd, clusterWidth := end+size, source.DisplayWidth(r)
		regional := r >= 0x1f1e6 && r <= 0x1f1ff
		join := false
		for clusterEnd < len(text) {
			next, nextSize := utf8.DecodeRuneInString(text[clusterEnd:])
			if nextSize == 0 {
				break
			}
			combining := source.DisplayWidth(next) == 0 || next == 0x200d || (next >= 0x1f3fb && next <= 0x1f3ff)
			if !combining && !join && !(regional && next >= 0x1f1e6 && next <= 0x1f1ff) {
				break
			}
			if next == 0xfe0f || join || regional {
				clusterWidth = 2
			}
			join = next == 0x200d
			regional = false
			clusterEnd += nextSize
		}
		if cells+clusterWidth > width {
			break
		}
		cells += clusterWidth
		end = clusterEnd
	}
	return displayMeasure{End: end, Cells: cells}
}
