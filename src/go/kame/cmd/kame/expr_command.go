package main

import (
	"kame/cli"
	"kame/lang/eval"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type exprEffectOutput struct { Out io.Writer; Err io.Writer }

func writeExprEffect(value any, effect eval.Effect) {
	output := value.(*exprEffectOutput)
	if effect.Kind == eval.EffectErr { output.Err.Write(effect.Data); return }
	if effect.Kind == eval.EffectOut || effect.Kind == eval.EffectYield { output.Out.Write(effect.Data) }
}

func runExpr(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	forwarded := cli.ExpressionRunArgs(args)
	defer slices.Free(mem.System, forwarded)
	return runSession(forwarded, in, out, errOut)
}
