package eval

import (
	"kame/core"
	"kame/host"
	"kame/lang/source"
	"solod.dev/so/slices"
)

func normalizeCapturePolicy(c *Context, policy core.Value, span source.Span) Result {
	if policy.Kind != core.Record {
		return failure(c.Run, "EXPR_INVALID", span, "capture policy requires a record")
	}
	limit := c.Program.CaptureLimit
	if limit <= 0 {
		limit = 1024 * 1024
	}
	stdout, stderr := false, false
	stdoutLimit, stderrLimit := int64(limit), int64(limit)
	for i := range policy.Record {
		field := policy.Record[i]
		if field.Key == "stdout" || field.Key == "stderr" {
			if field.Value.Kind != core.Bool {
				return failure(c.Run, "EXPR_INVALID", span, "capture stream selection requires a boolean")
			}
			if field.Key == "stdout" {
				stdout = field.Value.Bool
			} else {
				stderr = field.Value.Bool
			}
		} else if field.Key == "stdoutLimit" || field.Key == "stderrLimit" {
			if field.Value.Kind != core.Int || field.Value.Int <= 0 || field.Value.Int > 2147483647 {
				return failure(c.Run, "EXPR_INVALID", span, "capture limits require positive finite byte counts")
			}
			if field.Key == "stdoutLimit" {
				stdoutLimit = field.Value.Int
			} else {
				stderrLimit = field.Value.Int
			}
		} else {
			return failure(c.Run, "EXPR_INVALID", span, "unknown capture policy field")
		}
	}
	if !stdout && !stderr {
		return failure(c.Run, "EXPR_INVALID", span, "capture policy must select at least one stream")
	}
	fields := []core.RecordField{{Key: "stdout", Value: core.Value{Kind: core.Bool, Bool: stdout}}, {Key: "stderr", Value: core.Value{Kind: core.Bool, Bool: stderr}}, {Key: "stdoutLimit", Value: core.Value{Kind: core.Int, Int: stdoutLimit}}, {Key: "stderrLimit", Value: core.Value{Kind: core.Int, Int: stderrLimit}}}
	return Result{Value: core.NewRecord(c.Run, fields)}
}

func typedCapturedResult(c *Context, r Result, completion core.Value, policy core.Value, span source.Span) Result {
	names := []string{"stdout", "stderr"}
	for _, name := range names {
		selected := host.PayloadBool(policy, name)
		value := core.Value{}
		if selected {
			var raw core.Value
			for i := range completion.Record {
				if completion.Record[i].Key == name {
					raw = completion.Record[i].Value
				}
			}
			if raw.Kind == core.Bytes {
				value = raw.Clone(c.Run)
			}
			if value.Kind != core.Bytes {
				r.Free(c.Run)
				return failure(c.Run, "HOST_FAIL", span, "host omitted captured bytes")
			}
			if int64(len(value.Bytes)) > host.PayloadInt(policy, name+"Limit") {
				value.Free(c.Run)
				r.Free(c.Run)
				return failure(c.Run, "CAPTURE_LIMIT", span, "captured stream exceeded its byte limit")
			}
		}
		for i := range r.Value.Record {
			if r.Value.Record[i].Key == name+"Captured" {
				r.Value.Record[i].Value.Bool = selected
			}
		}
		r.Value.Record = slices.Append(c.Run, r.Value.Record, core.RecordField{Key: owned(c.Run, name), Value: value})
	}
	return r
}
