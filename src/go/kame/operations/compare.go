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
		freeArgCallables(c, v)
		return invalid()
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
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: !eq}}
}

func opLt(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord < 0}}
}

func opGt(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord > 0}}
}

func opGte(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord >= 0}}
}

func opLte(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	ord, ok := compareOrdered(v[0], v[1])
	if !ok {
		freeArgCallables(c, v)
		return invalid()
	}
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: ord <= 0}}
}

// compareEq implements strict, kind-aware equality. Numbers compare
// numerically across int/float; strings by exact bytes; nil/bool by value.
// Different kinds are unequal (not an error) except numbers. Lists, records,
// bytes, and patterns are EXPR_INVALID.
func compareEq(left core.Value, right core.Value) (bool, bool) {
	if left.Kind == core.Int || left.Kind == core.Float {
		if right.Kind != core.Int && right.Kind != core.Float {
			return false, true
		}
		return toFloat(left) == toFloat(right), true
	}
	if right.Kind == core.Int || right.Kind == core.Float {
		return false, true
	}
	if left.Kind != right.Kind {
		return false, true
	}
	switch left.Kind {
	case core.Nil:
		return true, true
	case core.Bool:
		return left.Bool == right.Bool, true
	case core.String:
		return left.Text == right.Text, true
	}
	return false, false
}

func toFloat(v core.Value) float64 {
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
		a, b := toFloat(left), toFloat(right)
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
