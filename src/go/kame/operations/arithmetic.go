package operations

import (
	"kame/core"
	"kame/lang/eval"
	"math"
)

func opArithmetic(c *eval.Context, _ any, values []core.Value) eval.Result {
	op := c.OperationName
	expect := "number"
	if op == "mod" {
		expect = "integer"
	}
	integers := true
	for i, value := range values {
		switch value.Kind {
		case core.Int:
		case core.Float:
			if op == "mod" {
				return invalidArgument(c, values, i, expect)
			}
			if math.IsNaN(value.Float) || math.IsInf(value.Float, 0) {
				discardArgCallables(c, values)
				return c.InvalidOperation("operands must be finite")
			}
			integers = false
		default:
			return invalidArgument(c, values, i, expect)
		}
	}

	if integers && op != "div" {
		left := values[0].Int
		var right int64
		if len(values) == 2 {
			right = values[1].Int
		}
		var result int64
		var overflow bool
		switch op {
		case "add":
			result = left + right
			overflow = (right > 0 && result < left) || (right < 0 && result > left)
		case "sub":
			result = left - right
			overflow = (right > 0 && result > left) || (right < 0 && result < left)
		case "mul":
			result = left * right
			overflow = (left == math.MinInt64 && right == -1) || (right != 0 && result/right != left)
		case "mod":
			if right == 0 {
				return c.InvalidOperation("cannot take a remainder by zero")
			}
			result = left % right
		case "neg":
			result, overflow = -left, left == math.MinInt64
		}
		if overflow {
			return c.InvalidOperation("integer result is outside the signed 64-bit range")
		}
		return eval.Result{Value: core.Value{Kind: core.Int, Int: result}}
	}

	number := func(value core.Value) float64 {
		if value.Kind == core.Int {
			return float64(value.Int)
		}
		return value.Float
	}
	left := number(values[0])
	var right float64
	if len(values) == 2 {
		right = number(values[1])
	}
	var result float64
	switch op {
	case "add":
		result = left + right
	case "sub":
		result = left - right
	case "mul":
		result = left * right
	case "div":
		if right == 0 {
			return c.InvalidOperation("cannot divide by zero")
		}
		result = left / right
	case "neg":
		result = -left
	}
	if math.IsInf(result, 0) {
		return c.InvalidOperation("floating-point result is outside the finite range")
	}
	return eval.Result{Value: core.Value{Kind: core.Float, Float: result}}
}
