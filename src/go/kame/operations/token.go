package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/strings"
	"solod.dev/so/unicode"
	"solod.dev/so/unicode/utf8"
)

type tokenRange struct{ first, last rune }

// Unicode 15.0.0 categories Nl and No, generated from Go unicode.Number minus
// unicode.Digit. Solod supplies the matching Letter/Digit tables, but no Number.
// Keep this table and the operation version aligned when upgrading Unicode.
var tokenNumbers = []tokenRange{
	{0xb2, 0xb3}, {0xb9, 0xb9}, {0xbc, 0xbe}, {0x9f4, 0x9f9},
	{0xb72, 0xb77}, {0xbf0, 0xbf2}, {0xc78, 0xc7e}, {0xd58, 0xd5e},
	{0xd70, 0xd78}, {0xf2a, 0xf33}, {0x1369, 0x137c}, {0x16ee, 0x16f0},
	{0x17f0, 0x17f9}, {0x19da, 0x19da}, {0x2070, 0x2070}, {0x2074, 0x2079},
	{0x2080, 0x2089}, {0x2150, 0x2182}, {0x2185, 0x2189}, {0x2460, 0x249b},
	{0x24ea, 0x24ff}, {0x2776, 0x2793}, {0x2cfd, 0x2cfd}, {0x3007, 0x3007},
	{0x3021, 0x3029}, {0x3038, 0x303a}, {0x3192, 0x3195}, {0x3220, 0x3229},
	{0x3248, 0x324f}, {0x3251, 0x325f}, {0x3280, 0x3289}, {0x32b1, 0x32bf},
	{0xa6e6, 0xa6ef}, {0xa830, 0xa835}, {0x10107, 0x10133}, {0x10140, 0x10178},
	{0x1018a, 0x1018b}, {0x102e1, 0x102fb}, {0x10320, 0x10323}, {0x10341, 0x10341},
	{0x1034a, 0x1034a}, {0x103d1, 0x103d5}, {0x10858, 0x1085f}, {0x10879, 0x1087f},
	{0x108a7, 0x108af}, {0x108fb, 0x108ff}, {0x10916, 0x1091b}, {0x109bc, 0x109bd},
	{0x109c0, 0x109cf}, {0x109d2, 0x109ff}, {0x10a40, 0x10a48}, {0x10a7d, 0x10a7e},
	{0x10a9d, 0x10a9f}, {0x10aeb, 0x10aef}, {0x10b58, 0x10b5f}, {0x10b78, 0x10b7f},
	{0x10ba9, 0x10baf}, {0x10cfa, 0x10cff}, {0x10e60, 0x10e7e}, {0x10f1d, 0x10f26},
	{0x10f51, 0x10f54}, {0x10fc5, 0x10fcb}, {0x11052, 0x11065}, {0x111e1, 0x111f4},
	{0x1173a, 0x1173b}, {0x118ea, 0x118f2}, {0x11c5a, 0x11c6c}, {0x11fc0, 0x11fd4},
	{0x12400, 0x1246e}, {0x16b5b, 0x16b61}, {0x16e80, 0x16e96}, {0x1d2c0, 0x1d2d3},
	{0x1d2e0, 0x1d2f3}, {0x1d360, 0x1d378}, {0x1e8c7, 0x1e8cf}, {0x1ec71, 0x1ecab},
	{0x1ecad, 0x1ecaf}, {0x1ecb1, 0x1ecb4}, {0x1ed01, 0x1ed2d}, {0x1ed2f, 0x1ed3d},
	{0x1f100, 0x1f10c},
}

func tokenCharacter(r rune) bool {
	if r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	low, high := 0, len(tokenNumbers)
	for low < high {
		mid := low + (high-low)/2
		if r < tokenNumbers[mid].first {
			high = mid
		} else if r > tokenNumbers[mid].last {
			low = mid + 1
		} else {
			return true
		}
	}
	return false
}

func opSubToken(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	for i := range values {
		if values[i].Kind != core.String {
			return invalidArgument(c, values, i, "string")
		}
	}
	from, to, text := values[0].Text, values[1].Text, values[2].Text
	if from == "" {
		return c.InvalidOperation("expects a non-empty search string")
	}
	b := strings.NewBuilder(c.Run)
	defer b.Free()
	written, scan := 0, 0
	for scan <= len(text)-len(from) {
		at := strings.Index(text[scan:], from)
		if at < 0 {
			break
		}
		at += scan
		end := at + len(from)
		left, right := false, false
		if at > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:at])
			left = tokenCharacter(r)
		}
		if end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = tokenCharacter(r)
		}
		if !left && !right {
			b.WriteString(text[written:at])
			b.WriteString(to)
			written, scan = end, end
		} else {
			_, width := utf8.DecodeRuneInString(text[at:])
			scan = at + width
		}
	}
	b.WriteString(text[written:])
	return eval.Result{Value: core.NewString(c.Run, b.String())}
}
