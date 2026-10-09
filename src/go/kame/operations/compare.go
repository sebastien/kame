package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/strings"
)

func opEq(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	eq, ok := compareEq(v[0], v[1])
	if !ok {
		discardArgCallables(c, v)
		return equalityFailure(c, v[0], v[1])
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: eq}}
}

func opIs(c *eval.Context, s any, v []core.Value) eval.Result {
	return opEq(c, s, v)
}

func opNe(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	eq, ok := compareEq(v[0], v[1])
	if !ok {
		discardArgCallables(c, v)
		return equalityFailure(c, v[0], v[1])
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: !eq}}
}

func opLt(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		discardArgCallables(c, v)
		return c.InvalidOperation("cannot order " + core.KindName(v[0].Kind) + " and " + core.KindName(v[1].Kind) + "; expects two numbers or two strings")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord < 0}}
}

func opGt(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		discardArgCallables(c, v)
		return c.InvalidOperation("cannot order " + core.KindName(v[0].Kind) + " and " + core.KindName(v[1].Kind) + "; expects two numbers or two strings")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord > 0}}
}

func opGte(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		discardArgCallables(c, v)
		return c.InvalidOperation("cannot order " + core.KindName(v[0].Kind) + " and " + core.KindName(v[1].Kind) + "; expects two numbers or two strings")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord >= 0}}
}

func opLte(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		discardArgCallables(c, v)
		return c.InvalidOperation("cannot order " + core.KindName(v[0].Kind) + " and " + core.KindName(v[1].Kind) + "; expects two numbers or two strings")
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord <= 0}}
}

func equalityFailure(c *eval.Context, left core.Value, right core.Value) eval.Result {
	return c.InvalidOperation("cannot compare equality of " + core.KindName(left.Kind) + " and " + core.KindName(right.Kind) + "; expects nil, bool, number, string, bytes, or recursively comparable lists/records (unique keys, nesting below 256); opaque values are unsupported")
}

// Validate both complete operands before comparison: unequal shapes or earlier
// items must not hide an unsupported nested value. Match portable JSON/signature
// depth limits; invalid or cyclic host-created containers fail rather than recurse
// without bound. This does not allocate or serialize the operands.
func compareEq(left core.Value, right core.Value) (bool, bool) {
	if !equalitySupported(left, 0) || !equalitySupported(right, 0) { return false, false }
	return equalValues(left, right), true
}

func equalitySupported(value core.Value, depth int) bool {
	if depth >= 256 { return false }
	switch value.Kind {
	case core.Nil, core.Bool, core.Int, core.Float, core.String, core.Bytes:
		return true
	case core.List:
		for i := range value.List { if !equalitySupported(value.List[i], depth+1) { return false } }
		return true
	case core.Record:
		for i := range value.Record {
			if !equalitySupported(value.Record[i].Value, depth+1) { return false }
			for j := 0; j < i; j++ { if value.Record[i].Key == value.Record[j].Key { return false } }
		}
		return true
	}
	return false
}

func equalValues(left core.Value, right core.Value) bool {
	if left.Kind == core.Int && right.Kind == core.Float { return equalIntFloat(left.Int, right.Float) }
	if left.Kind == core.Float && right.Kind == core.Int { return equalIntFloat(right.Int, left.Float) }
	if left.Kind != right.Kind { return false }
	switch left.Kind {
	case core.Nil:
		return true
	case core.Bool:
		return left.Bool == right.Bool
	case core.Int:
		return left.Int == right.Int
	case core.Float:
		return left.Float == right.Float
	case core.String:
		return left.Text == right.Text
	case core.Bytes:
		if len(left.Bytes) != len(right.Bytes) { return false }
		for i := range left.Bytes { if left.Bytes[i] != right.Bytes[i] { return false } }
		return true
	case core.List:
		if len(left.List) != len(right.List) { return false }
		for i := range left.List { if !equalValues(left.List[i], right.List[i]) { return false } }
		return true
	case core.Record:
		if len(left.Record) != len(right.Record) { return false }
		// ponytail: quadratic key matching avoids allocating an index; add one
		// only if comparisons of large records become a measured bottleneck.
		for i := range left.Record {
			found := false
			for j := range right.Record {
				if left.Record[i].Key != right.Record[j].Key { continue }
				if !equalValues(left.Record[i].Value, right.Record[j].Value) { return false }
				found = true
				break
			}
			if !found { return false }
		}
		return true
	}
	return false
}

func equalIntFloat(integer int64, floating float64) bool {
	// Guard conversion at the int64 bounds (and reject NaN/infinity) before
	// comparing both representations. In particular 2^53+1 must not round to 2^53.
	return floating >= -9223372036854775808.0 && floating < 9223372036854775808.0 && int64(floating) == integer && float64(integer) == floating
}

// orderingNumber preserves ordering's float64 coercion policy: large integers
// can round to the same value. Equality deliberately uses exact integer checks.
func orderingNumber(v core.Value) float64 {
	if v.Kind == core.Float {
		return v.Float
	}
	return float64(v.Int)
}

// compareOrdered orders numbers numerically and strings by UTF-8 bytes.
// Mixed kinds and non-scalars are invalid.
func compareOrdered(left core.Value, right core.Value) (int, bool) {
	leftNum := left.Kind == core.Int || left.Kind == core.Float
	rightNum := right.Kind == core.Int || right.Kind == core.Float
	if leftNum && rightNum {
		a, b := orderingNumber(left), orderingNumber(right)
		if a < b {
			return -1, true
		}
		if a > b {
			return 1, true
		}
		return 0, true
	}
	if left.Kind == core.String && right.Kind == core.String {
		return strings.Compare(left.Text, right.Text), true
	}
	return 0, false
}
