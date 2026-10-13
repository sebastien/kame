package eval

import "kame/lang/source"

// Missing library names keep the registered diagnostic codes and authored spans.
// Lexical/custom bindings are resolved first, so user callable names are not
// treated as standard-library aliases or renamed by their effect category.
func missingOperation(c *Context, name string, span source.Span, reference bool) Result {
	code := "OP_UNKNOWN"
	if reference { code = "REF_MISSING" }
	if name == "cat" {
		return failure(c.Run, code, span, "Kame cat was removed; use str for scalar text, join for collections, or json for serialization")
	}
	switch name {
	case "write", "yield", "out", "err", "run", "pipe", "shell", "sh", "shellrun", "shell-template", "fail":
		return failure(c.Run, code, span, "operation `"+name+"` was renamed to `"+name+"!`; action names require an explicit ! suffix")
	}
	if reference { return failure(c.Run, code, span, "unknown reference: "+name) }
	return failure(c.Run, code, span, "unknown operation: "+name)
}
