package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

type publicationState struct {
	ID         int64
	Done       bool
	Value      core.Value
	Diagnostic diagnostic.Diagnostic
}

func freePublicationState(a mem.Allocator, value any) {
	state := value.(*publicationState)
	state.Value.Free(a)
	state.Diagnostic.Free(a)
	mem.Free(a, state)
}

func publicationRequest(c *eval.Context, payload core.Value) eval.Result {
	defer payload.Free(c.Run)
	if c.Engine == nil || c.Program == nil {
		return eval.Result{Waiting: true}
	}
	var state *publicationState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*publicationState)
	}
	if state == nil {
		state = mem.Alloc[publicationState](c.Program.Alloc)
		c.SetOperationState(state, freePublicationState)
	}
	if state.Done {
		return eval.Result{Value: state.Value.Clone(c.Run), Diagnostic: state.Diagnostic.Clone(c.Run)}
	}
	if state.ID == 0 {
		state.ID = c.Submit(host.RequestPublishFile, payload)
		if state.ID == 0 {
			return failure("HOST_FAIL", "publication request was not accepted")
		}
		return eval.Result{Waiting: true}
	}
	if c.Completion().RequestID != state.ID {
		return eval.Result{Waiting: true}
	}
	completion := c.TakeCompletion()
	state.Value = completion.Value.Clone(c.Program.Alloc)
	state.Diagnostic = completion.Diagnostic.Clone(c.Program.Alloc)
	state.Done = true
	completion.Value.Free(c.Run)
	completion.Diagnostic.Free(c.Run)
	return eval.Result{Value: state.Value.Clone(c.Run), Diagnostic: state.Diagnostic.Clone(c.Run)}
}

func publicationPath(value core.Value) string {
	if value.Kind == core.String {
		return value.Text
	}
	if value.Kind == core.Resource && value.Resource.Kind == core.ResourceFile {
		return value.Resource.Name
	}
	return ""
}

func opPublish(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	staged, destination := publicationPath(values[0]), publicationPath(values[1])
	if staged == "" {
		return invalidArgument(c, values, 0, "non-empty file path or resource")
	}
	if destination == "" {
		return invalidArgument(c, values, 1, "non-empty file path or resource")
	}
	if !c.Allows(eval.Read, staged) || !c.Allows(eval.Write, staged) || !c.Allows(eval.Write, destination) {
		return failure("CAP_DENIED", "publication access denied")
	}
	for i := range c.RuleFrames {
		if c.RuleFrames[i].ServiceRule {
			return failure("EXPR_INVALID", "services cannot publish file outputs")
		}
	}
	paths := []string{staged, destination}
	for i := range paths {
		if strings.Contains(paths[i], "://") && !strings.HasPrefix(paths[i], "file://") {
			return failure("FS_UNSUPPORTED", "publication supports local files only")
		}
	}
	if c.Program != nil && c.Program.DryRun {
		return eval.Result{}
	}
	stagedPath, destinationPath := c.CanonicalPath(staged), c.CanonicalPath(destination)
	defer mem.FreeString(c.Run, stagedPath)
	defer mem.FreeString(c.Run, destinationPath)
	r := publicationRequest(c, host.PublishPayload(c.Run, stagedPath, destinationPath))
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	code := recordField(r.Value, "code")
	published, removed := recordField(r.Value, "published"), recordField(r.Value, "stagedRemoved")
	if code.Kind != core.String || published.Kind != core.Bool || removed.Kind != core.Bool || (code.Text == "" && (!published.Bool || !removed.Bool)) || (code.Text == "FS_PUBLISHED" && !published.Bool) {
		r.Free(c.Run)
		return failure("HOST_FAIL", "publication completion omitted a valid outcome")
	}
	if code.Text != "" {
		if published.Bool {
			code.Text = "FS_PUBLISHED"
		}
		failed := ownedFailure(c.Run, code.Text, "cannot publish staged file")
		if code.Text == "FS_PUBLISHED" {
			failed.Diagnostic.Cause.Kind = core.NewString(c.Run, "publication").Text
			failed.Diagnostic.Cause.HasPublication = true
			failed.Diagnostic.Cause.Published = true
			failed.Diagnostic.Cause.StagedRemoved = removed.Bool
		}
		r.Free(c.Run)
		return failed
	}
	r.Free(c.Run)
	return eval.Result{Value: core.NewRecord(c.Run, []core.RecordField{
		{Key: "published", Value: core.Value{Kind: core.Bool, Bool: true}},
		{Key: "stagedRemoved", Value: core.Value{Kind: core.Bool, Bool: true}},
	})}
}
