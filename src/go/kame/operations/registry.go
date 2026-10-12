package operations

import "kame/lang/eval"

const version = "v12"

// Register describes every standard operation explicitly. Categories describe
// the operation itself; callable/template effects still propagate dynamically.
func Register(r *eval.Registry) bool {
	return pure(r, "not", opNot, 1, 1) && pure(r, "bool", opBool, 1, 1) &&
		contract(r, "fail!", opFail, 1, 1, eval.ControlOperation, eval.AllPhases, eval.RaisesFailure) &&
		pure(r, "parse-json", opParseJSON, 1, 1) && pure(r, "str", opStr, 0, -1) && pure(r, "json", opJSON, 1, 1) && pure(r, "count", opCount, 1, 1) &&
		pure(r, "eq", opEq, 2, 2) && pure(r, "is", opIs, 2, 2) &&
		pure(r, "ne", opNe, 2, 2) && pure(r, "lt", opLt, 2, 2) &&
		pure(r, "gt", opGt, 2, 2) && pure(r, "gte", opGte, 2, 2) && pure(r, "lte", opLte, 2, 2) &&
		pure(r, "template-cat", opTemplateCat, 0, -1) && pure(r, "text", opText, 1, 1) &&
		registerRender(r) && pure(r, "template-items", opTemplateItems, 1, 1) &&
		callback(r, "template-apply", opTemplateApply, 2, 2) &&
		pure(r, "template-trim-start", opTemplateTrimStart, 1, 1) && pure(r, "template-trim-end", opTemplateTrimEnd, 1, 1) &&
		contract(r, "tool", opTool, 1, 1, eval.ObservationOperation, eval.AllPhases, eval.TracksDependency) &&
		pure(r, "get", opGet, 2, 3) && pure(r, "first", opFirst, 1, 1) && pure(r, "nth", opNth, 2, 2) &&
		callback(r, "apply", opApply, 2, 2) && pure(r, "list", opList, 0, -1) &&
		pure(r, "items", opItems, 1, 1) && pure(r, "set-field", opSetField, 3, 3) && pure(r, "merge", opMerge, 0, -1) &&
		pure(r, "record?", opKindPredicate, 1, 1) && pure(r, "list?", opKindPredicate, 1, 1) && pure(r, "string?", opKindPredicate, 1, 1) &&
		pure(r, "digest", opDigest, 2, 2) &&
		callback(r, "map", opMap, 2, 2) && callback(r, "flatmap", opFlatMap, 2, 2) &&
		callback(r, "filter", opFilter, 2, 2) && callback(r, "filter-out", opFilterOut, 2, 2) && callback(r, "reduce", opReduce, 2, 3) &&
		pure(r, "concat", opConcat, 0, -1) && pure(r, "slice", opSlice, 2, 3) && pure(r, "sorted", opSorted, 1, 1) &&
		pure(r, "unique", opUnique, 1, 1) && pure(r, "join", opJoin, 2, 2) && pure(r, "resource", opResource, 1, 1) &&
		pure(r, "split", opSplit, 2, 2) && pure(r, "strip", opStrip, 1, 1) && pure(r, "pattern", opPattern, 1, 1) &&
		pure(r, "replace", opReplace, 2, 3) && pure(r, "includes?", opIncludes, 2, 2) &&
		pure(r, "regex-match", opRegexMatch, 2, 2) && pure(r, "regex-replace", opRegexReplace, 3, 3) && pure(r, "capture", opCapture, 2, 2) &&
		pure(r, "starts?", opStarts, 2, 2) && pure(r, "ends?", opEnds, 2, 2) &&
		pure(r, "black", opTerminalStyle, 1, 1) && pure(r, "red", opTerminalStyle, 1, 1) &&
		pure(r, "green", opTerminalStyle, 1, 1) && pure(r, "yellow", opTerminalStyle, 1, 1) &&
		pure(r, "blue", opTerminalStyle, 1, 1) && pure(r, "magenta", opTerminalStyle, 1, 1) &&
		pure(r, "cyan", opTerminalStyle, 1, 1) && pure(r, "white", opTerminalStyle, 1, 1) &&
		pure(r, "bold", opTerminalStyle, 1, 1) && pure(r, "dim", opTerminalStyle, 1, 1) &&
		pure(r, "uppercase", opUppercase, 1, 1) && pure(r, "lowercase", opLowercase, 1, 1) &&
		pure(r, "basename", opBasename, 1, 1) && pure(r, "dirname", opDirname, 1, 1) &&
		pure(r, "splitext", opSplitext, 1, 1) && pure(r, "ext", opExt, 1, 1) &&
		pure(r, "joinpath", opJoinpath, 0, -1) && pure(r, "relpath", opRelpath, 2, 2) && pure(r, "abspath", opAbspath, 1, 1) &&
		observation(r, "read", opRead, 1, 1, eval.Read) && observation(r, "exists?", opExists, 1, 1, eval.Read) &&
		observation(r, "stat", opStat, 1, 1, eval.Read) && observation(r, "lstat", opLstat, 1, 1, eval.Read) &&
		observation(r, "readlink", opReadlink, 1, 1, eval.Read) && observation(r, "wildcard", opWildcard, 1, -1, eval.Read) &&
		registerWrite(r) && observation(r, "env", opEnv, 1, 1, eval.Env) &&
		process(r, "shell!", opShell, 1, 2) && process(r, "sh!", opShell, 1, 2) && process(r, "shellrun!", opShell, 1, 2) &&
		process(r, "shell-template!", opShellTemplate, 2, 2) &&
		contract(r, "out!", opOut, 1, -1, eval.OutputOperation, eval.RuntimePhases, eval.EmitsOutput) &&
		contract(r, "err!", opErr, 1, -1, eval.OutputOperation, eval.RuntimePhases, eval.EmitsOutput) &&
		contract(r, "yield!", opYield, 1, -1, eval.OutputOperation, eval.RuntimePhases, eval.PublishesFile) &&
		pure(r, "nop", opNop, 0, -1) &&
		contract(r, "now", opNow, 0, 0, eval.VolatileOperation, eval.RuntimePhases, eval.ReadsClock) &&
		contract(r, "monotonic", opMonotonic, 0, 0, eval.VolatileOperation, eval.RuntimePhases, eval.ReadsClock)
}

func contract(r *eval.Registry, name string, call eval.OperationCall, min int, max int, category eval.OperationCategory, phases eval.PhaseMask, behavior eval.OperationBehavior) bool {
	return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Category: category, Phases: phases, Behavior: behavior, Version: version})
}

func pure(r *eval.Registry, name string, call eval.OperationCall, min int, max int) bool {
	return contract(r, name, call, min, max, eval.PureOperation, eval.AllPhases, 0)
}

func callback(r *eval.Registry, name string, call eval.OperationCall, min int, max int) bool {
	return contract(r, name, call, min, max, eval.PureOperation, eval.AllPhases, eval.CallsCallable)
}

func observation(r *eval.Registry, name string, call eval.OperationCall, min int, max int, capability eval.Capability) bool {
	return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Capabilities: []eval.Capability{capability}, Category: eval.ObservationOperation, Phases: eval.AllPhases, Behavior: eval.ReadsHost | eval.TracksDependency, Version: version})
}

func process(r *eval.Registry, name string, call eval.OperationCall, min int, max int) bool {
	return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Capabilities: []eval.Capability{eval.Run}, Category: eval.ProcessOperation, Phases: eval.EvaluationAllowed, Behavior: eval.StartsProcess | eval.TracksDependency, Version: version})
}

func registerRender(r *eval.Registry) bool {
	return r.Add(eval.Operation{Name: "render", Call: opRender, MinArity: 1, MaxArity: 3, ConditionalCapabilities: []eval.Capability{eval.Read}, Category: eval.ObservationOperation, Phases: eval.AllPhases, Behavior: eval.ReadsHost | eval.TracksDependency | eval.CallsCallable, Version: version})
}

func registerWrite(r *eval.Registry) bool {
	return r.Add(eval.Operation{Name: "write!", Call: opWrite, MinArity: 2, MaxArity: 2, Capabilities: []eval.Capability{eval.Write}, Category: eval.OutputOperation, Phases: eval.RuntimePhases, Behavior: eval.PublishesFile, Version: version})
}
