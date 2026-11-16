package eval

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/source"
)

// TerminalAuthority is a recipe's terminal-handoff grant from rule metadata
// (051-kash-session-authority.md). StageTTY and StageForeground record whether
// any stage in the recipe sets the facility explicitly; then metadata only
// authorizes, and exactly the stages that request it receive it.
type TerminalAuthority struct {
	TTY             bool
	Foreground      bool
	StageTTY        bool
	StageForeground bool
}

// setupFlag is a stage's explicit boolean setup: unset, false, or true.
type setupFlag uint8

const (
	setupUnset setupFlag = iota
	setupFalse
	setupTrue
)

// Payload keys: the request-level handoff and each stage's resolved roles.
const (
	terminalKey           = "terminal"
	terminalRequiredKey   = "terminalRequired"
	terminalAutoKey       = "foregroundAuto"
	terminalAttachKey     = "terminalAttach"
	terminalForegroundKey = "terminalForeground"
	terminalBackgroundKey = "terminalBackground"
)

func setupBoolean(value core.Value) (bool, bool) {
	if value.Kind == core.Bool {
		return value.Bool, true
	}
	if value.Kind == core.String && (value.Text == ":true" || value.Text == ":false") {
		return value.Text == ":true", true
	}
	return false, false
}

// terminalSetup records one stage's :tty or :foreground. Requesting either
// facility needs metadata authority, or an interactive terminal session.
func (p *Program) terminalSetup(state *captureState, kind expr.SetupKey, value core.Value, span source.Span, c *Context) Result {
	name, granted := "tty", c.Terminal.TTY
	if kind == expr.SetupForeground {
		name, granted = "foreground", c.Terminal.Foreground
	}
	enabled, ok := setupBoolean(value)
	if !ok {
		return failure(c.Run, "EXPR_INVALID", span, name+" setup requires :true or :false")
	}
	if enabled && !granted && !p.Terminal {
		r := failure(c.Run, "CAP_DENIED", span, "terminal "+name+" is not authorized for this execution")
		r.Diagnostic.Tips = append(r.Diagnostic.Tips, owned(c.Run, "authorize it in the rule metadata, e.g. ["+name+": :true]; direct sessions cannot grant it"))
		return r
	}
	flag := setupFalse
	if enabled {
		flag = setupTrue
	}
	if kind == expr.SetupTTY {
		state.TTY = flag
	} else {
		state.Foreground = flag
	}
	return Result{}
}

// terminalStageSetup moves the current stage's explicit flags into its setup.
func terminalStageSetup(state *captureState, setup *core.Value) {
	if state.TTY != setupUnset {
		setup.Record = append(setup.Record, core.RecordField{Key: owned(state.Alloc, "tty"), Value: core.Value{Kind: core.Bool, Bool: state.TTY == setupTrue}})
	}
	if state.Foreground != setupUnset {
		setup.Record = append(setup.Record, core.RecordField{Key: owned(state.Alloc, "foreground"), Value: core.Value{Kind: core.Bool, Bool: state.Foreground == setupTrue}})
	}
	state.TTY, state.Foreground = setupUnset, setupUnset
}

func stageFlag(setup core.Value, key string) setupFlag {
	for i := range setup.Record {
		if setup.Record[i].Key == key && setup.Record[i].Value.Kind == core.Bool {
			if setup.Record[i].Value.Bool {
				return setupTrue
			}
			return setupFalse
		}
	}
	return setupUnset
}

// terminalPayload resolves which stages of one graph receive the terminal.
// An interactive session lends it to every stage of a foreground graph and
// falls back to pipes without one; rule metadata lends it per 051 and
// requires it. Only eligible (foreground statement) graphs take it.
func (p *Program) terminalPayload(state *captureState, payload *core.Value, eligible bool, span source.Span, c *Context) Result {
	session, authority := p.Terminal, c.Terminal
	if !session && !authority.TTY {
		return Result{}
	}
	attachDefault := session || !authority.StageTTY
	foregroundAllowed := session || authority.Foreground
	foregroundAuto := foregroundAllowed && (session || !authority.StageForeground)
	attached, explicit := false, false
	foreground := -1
	attach := make([]bool, len(state.Setups))
	for i := range state.Setups {
		tty, fore := stageFlag(state.Setups[i], "tty"), stageFlag(state.Setups[i], "foreground")
		explicit = explicit || tty == setupTrue || fore == setupTrue
		attach[i] = tty == setupTrue || (tty == setupUnset && attachDefault)
		attached = attached || attach[i]
		if fore == setupTrue {
			if !attach[i] {
				return failure(c.Run, "EXPR_INVALID", span, "a foreground stage must also use the terminal")
			}
			if foreground >= 0 {
				return failure(c.Run, "EXPR_INVALID", span, "only one stage can hold the terminal foreground")
			}
			foreground = i
		}
	}
	if !eligible {
		if explicit {
			return failure(c.Run, "EXPR_INVALID", span, "only foreground statements can use the terminal; captures, background work and cleanup keep their pipes")
		}
		return Result{}
	}
	if !attached {
		return Result{}
	}
	for i := range state.Setups {
		setup := &state.Setups[i]
		setup.Record = append(setup.Record, core.RecordField{Key: owned(state.Alloc, terminalAttachKey), Value: core.Value{Kind: core.Bool, Bool: attach[i]}})
		setup.Record = append(setup.Record, core.RecordField{Key: owned(state.Alloc, terminalForegroundKey), Value: core.Value{Kind: core.Bool, Bool: i == foreground}})
	}
	payload.Record = append(payload.Record,
		core.RecordField{Key: owned(c.Run, terminalKey), Value: core.Value{Kind: core.Bool, Bool: true}},
		core.RecordField{Key: owned(c.Run, terminalRequiredKey), Value: core.Value{Kind: core.Bool, Bool: !session}},
		core.RecordField{Key: owned(c.Run, terminalAutoKey), Value: core.Value{Kind: core.Bool, Bool: foregroundAuto && foreground < 0}})
	if c.TerminalBackground {
		payload.Record = append(payload.Record, core.RecordField{Key: owned(c.Run, terminalBackgroundKey), Value: core.Value{Kind: core.Bool, Bool: true}})
	}
	return Result{}
}
