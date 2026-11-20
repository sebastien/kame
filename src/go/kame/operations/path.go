package operations

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/eval"
	"path"

	"strings"
)

func opBasename(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	value, ok := v[0].Text, v[0].IsPathText()
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	return eval.Result{Value: core.NewString(c.Run, path.Base(value))}
}
func opDirname(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := v[0].Text, v[0].IsPathText()
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	result := path.Dir(value)
	if v[0].Kind == core.Path {
		switch result {
		case "~", "..":
			marked := cloneText(c.Run, result+"/")
			mem.FreeString(c.Run, result)
			result = marked
		default:
			if !path.IsAbs(result) && !strings.HasPrefix(result, "../") && !strings.HasPrefix(result, "~/") {
				marked := explicitRelativePath(c.Run, result)
				mem.FreeString(c.Run, result)
				result = marked
			}
		}
	}
	return eval.Result{Value: core.Value{Kind: v[0].Kind, Text: result}}
}
func extension(value string) string {
	base := path.Base(value)
	if len(base) != 0 && base[0] == '.' && strings.Index(base[1:], ".") < 0 {
		return ""
	}
	return path.Ext(value)
}
func opSplitext(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := v[0].Text, v[0].IsPathText()
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	suffix := extension(value)
	root := value[:len(value)-len(suffix)]
	result := make([]core.Value, 2)
	result[0] = core.NewString(c.Run, root)
	result[0].Kind = v[0].Kind
	result[1] = core.NewString(c.Run, suffix)
	out := core.NewList(c.Run, result)
	freeValues(c, result)
	return eval.Result{Value: out}
}
func opExt(c *eval.Context, s any, v []core.Value) eval.Result {
	_, _ = c, s
	value, ok := v[0].Text, v[0].IsPathText()
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	return eval.Result{Value: core.NewString(c.Run, extension(value))}
}
func opJoinpath(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	var values []string
	kind := core.String
	for i := range v {
		if !v[i].IsPathText() {
			mem.FreeSlice(nil, values)
			return invalidArgument(c, v, i, "string")
		}
		if v[i].Kind == core.Path {
			kind = core.Path
		}
		values = append(values, v[i].Text)
	}
	joined := path.Join(values...)
	// Cleaning must not turn an explicitly relative file into a bare task name.
	first := 0
	for first < len(values) && values[first] == "" {
		first++
	}
	if first < len(values) && (values[first] == "." || strings.HasPrefix(values[first], "./")) && !path.IsAbs(joined) && joined != ".." && !strings.HasPrefix(joined, "../") {
		marked := explicitRelativePath(c.Run, joined)
		mem.FreeString(c.Run, joined)
		joined = marked
	}
	mem.FreeSlice(nil, values)
	return eval.Result{Value: core.Value{Kind: kind, Text: joined}}
}
func opAbspath(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value, ok := v[0].Text, v[0].IsPathText()
	if !ok {
		return invalidArgument(c, v, 0, "string")
	}
	if path.IsAbs(value) {
		return eval.Result{Value: core.Value{Kind: v[0].Kind, Text: path.Clean(value)}}
	}
	return eval.Result{Value: core.Value{Kind: v[0].Kind, Text: path.Join(c.Cwd, value)}}
}
func opRelpath(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	target, ok := v[1].Text, v[1].IsPathText()
	if !ok {
		return invalidArgument(c, v, 1, "string")
	}
	if !v[0].IsPathText() {
		return invalidArgument(c, v, 0, "string")
	}
	base := path.Join(c.Cwd, v[0].Text)
	absolute := path.Join(c.Cwd, target)
	defer mem.FreeString(c.Run, base)
	defer mem.FreeString(c.Run, absolute)
	start := commonPathPrefix(base, absolute)
	b := strings.Builder{}
	defer b.Reset()
	for i := start; i < len(base); {
		for i < len(base) && base[i] == '/' {
			i++
		}
		if i == len(base) {
			break
		}
		for i < len(base) && base[i] != '/' {
			i++
		}
		if b.Len() != 0 {
			b.WriteByte('/')
		}
		b.WriteString("..")
	}
	suffix := absolute[start:]
	for len(suffix) != 0 && suffix[0] == '/' {
		suffix = suffix[1:]
	}
	if suffix != "" {
		if b.Len() != 0 {
			b.WriteByte('/')
		}
		b.WriteString(suffix)
	}
	if b.Len() == 0 {
		b.WriteByte('.')
	}
	result := b.String()
	if v[0].Kind == core.Path || v[1].Kind == core.Path {
		if result == ".." || strings.HasPrefix(result, "../") {
			return eval.Result{Value: core.NewPath(c.Run, result)}
		}
		return eval.Result{Value: core.Value{Kind: core.Path, Text: explicitRelativePath(c.Run, result)}}
	}
	return eval.Result{Value: core.NewString(c.Run, result)}
}

func explicitRelativePath(a mem.Allocator, text string) string {
	if text == "." {
		return cloneText(a, "./")
	}
	return cloneText(a, "./"+text)
}
func commonPathPrefix(left string, right string) int {
	i := 0
	for i < len(left) && i < len(right) && left[i] == right[i] {
		i++
	}
	if i == len(left) && (i == len(right) || right[i] == '/') {
		return i
	}
	if i == len(right) && (i == len(left) || left[i] == '/') {
		return i
	}
	for i > 0 && left[i-1] != '/' {
		i--
	}
	return i
}
