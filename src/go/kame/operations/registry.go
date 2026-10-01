package operations

import "kame/lang/eval"

const version = "v1"

// Register installs every standard operation. Registration stays declarative
// so operation implementations can be organized by domain independently.
func Register(r *eval.Registry) bool {
	return add(r, "not", opNot, 1, 1) && add(r, "bool", opBool, 1, 1) &&
		add(r, "fail", opFail, 1, 1) && add(r, "parse-json", opParseJSON, 1, 1) && add(r, "str", opStr, 1, 1) && add(r, "count", opCount, 1, 1) &&
		add(r, "eq", opEq, 2, 2) && add(r, "is", opIs, 2, 2) &&
		add(r, "ne", opNe, 2, 2) && add(r, "lt", opLt, 2, 2) &&
		add(r, "gt", opGt, 2, 2) && add(r, "gte", opGte, 2, 2) &&
		add(r, "lte", opLte, 2, 2) &&
		add(r, "cat", opCat, 0, -1) && add(r, "text", opText, 1, 1) &&
		add(r, "render", opRender, 1, 3) &&
		add(r, "first", opFirst, 1, 1) && add(r, "nth", opNth, 2, 2) &&
		add(r, "apply", opApply, 2, 2) && add(r, "list", opList, 0, -1) &&
		add(r, "map", opMap, 2, 2) && add(r, "flatmap", opFlatMap, 2, 2) &&
		add(r, "filter", opFilter, 2, 2) && add(r, "filter-out", opFilterOut, 2, 2) &&
		add(r, "reduce", opReduce, 2, 3) && add(r, "concat", opConcat, 0, -1) &&
		add(r, "slice", opSlice, 2, 3) && add(r, "sorted", opSorted, 1, 1) &&
		add(r, "unique", opUnique, 1, 1) && add(r, "join", opJoin, 2, 2) &&
		add(r, "split", opSplit, 2, 2) && add(r, "strip", opStrip, 1, 1) &&
		add(r, "replace", opReplace, 2, 3) && add(r, "includes?", opIncludes, 2, 2) &&
		add(r, "starts?", opStarts, 2, 2) && add(r, "ends?", opEnds, 2, 2) &&
		add(r, "uppercase", opUppercase, 1, 1) && add(r, "lowercase", opLowercase, 1, 1) &&
		add(r, "basename", opBasename, 1, 1) && add(r, "dirname", opDirname, 1, 1) &&
		add(r, "splitext", opSplitext, 1, 1) && add(r, "ext", opExt, 1, 1) &&
		add(r, "joinpath", opJoinpath, 0, -1) && add(r, "relpath", opRelpath, 2, 2) &&
		add(r, "abspath", opAbspath, 1, 1) &&
		addCapability(r, "read", opRead, 1, 1, eval.Read) &&
		addCapability(r, "exists?", opExists, 1, 1, eval.Read) &&
		addCapability(r, "stat", opStat, 1, 1, eval.Read) &&
		addCapability(r, "wildcard", opWildcard, 1, 1, eval.Read) &&
		addCapability(r, "write", opWrite, 2, 2, eval.Write) &&
		addCapability(r, "env", opEnv, 1, 1, eval.Env) &&
		addCapability(r, "shell", opShell, 1, 2, eval.Run) &&
		add(r, "out", opOut, 1, -1) && add(r, "err", opErr, 1, -1) &&
		add(r, "yield", opYield, 1, -1) && add(r, "nop", opNop, 0, -1) &&
		add(r, "now", opNow, 0, 0) && add(r, "monotonic", opMonotonic, 0, 0)
}

func add(r *eval.Registry, name string, call eval.OperationCall, min int, max int) bool {
	return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Version: version})
}

func addCapability(r *eval.Registry, name string, call eval.OperationCall, min int, max int, capability eval.Capability) bool {
	return r.Add(eval.Operation{Name: name, Call: call, MinArity: min, MaxArity: max, Capabilities: []eval.Capability{capability}, Version: version})
}
