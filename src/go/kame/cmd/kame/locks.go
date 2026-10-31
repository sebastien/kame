package main

import (
	"kame/cli"
	"kame/host/posix"
	"solod.dev/so/bytes"
	"solod.dev/so/encoding/json"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

var lockOut io.Writer
var lockErr io.Writer
var lockLastNotice int64
var lockWaiting bool
var busyLock posix.LockEvent

func lockCommand(args []string, command string) string {
	var text = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&text, "kame")
	if command == "" {
		io.WriteString(&text, " do clean")
	}
	// Values (including --define/--env and NAME=VALUE) are deliberately omitted.
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if cli.OptionTakesValue(arg) {
			i++
			continue
		}
		if strings.Contains(arg, "=") {
			continue
		}
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "--watch" {
			continue
		}
		if command == "" {
			continue
		}
		io.WriteString(&text, " "+arg)
	}
	result := cloneCommandText(text.String())
	text.Free()
	return result
}

func finishLockPresentation() {
	posix.ObserveLocks(nil, "kame")
	mem.FreeString(mem.System, busyLock.Path)
	mem.FreeString(mem.System, busyLock.OwnerCommand)
	busyLock = posix.LockEvent{}
	lockOut, lockErr = nil, nil
}

func writeLockFields(e *json.Encoder, event *posix.LockEvent) {
	e.Str("kind")
	e.Str(event.Kind)
	e.Str("path")
	e.Str(event.Path)
	e.Str("elapsedMs")
	e.Int(event.ElapsedMS)
	e.Str("owner")
	if event.OwnerPID == 0 {
		e.Null()
		return
	}
	e.BeginObject()
	e.Str("pid")
	e.Int(event.OwnerPID)
	e.Str("command")
	e.Str(event.OwnerCommand)
	e.Str("verified")
	e.Bool(event.OwnerVerified)
	if event.HeldMS >= 0 {
		e.Str("heldMs")
		e.Int(event.HeldMS)
	}
	e.EndObject()
}

func writeLockText(out io.Writer, text string) {
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] < 32 || text[i] == 127 {
			io.WriteString(out, text[start:i])
			io.WriteString(out, "?")
			start = i + 1
		}
	}
	io.WriteString(out, text[start:])
}

func writeLockOwner(out io.Writer, event *posix.LockEvent) {
	if event.OwnerPID == 0 {
		io.WriteString(out, "unavailable")
		return
	}
	fmt.Fprintf(out, "PID %d", event.OwnerPID)
	if event.OwnerCommand != "" {
		io.WriteString(out, " — ")
		writeLockText(out, event.OwnerCommand)
	}
	if !event.OwnerVerified {
		io.WriteString(out, " (recorded)")
	}
}

func writeLockRow(out io.Writer, label string, value string, token string, width int) {
	io.WriteString(out, "  ")
	labelToken := "text.muted"
	if label == "Ctrl-C" {
		labelToken = "message.tip"
	}
	writeStyledPadded(out, label, 8, labelToken)
	var field = bytes.NewBuffer(mem.System, nil)
	if width > 0 {
		writeDashboardField(&field, value, width-12)
	} else {
		writeLockText(&field, value)
	}
	cli.Style(out, token, field.String(), diagnosticColor == "always")
	io.WriteString(out, "\n")
	field.Free()
}

func writeLockHeading(out io.Writer, event *posix.LockEvent, width int) {
	status, detail := "! BLOCKED", "waiting for "+event.Kind+" lock"
	if event.State == "busy" {
		status, detail = "! BUSY", "exclusive "+event.Kind+" lock"
	}
	var text = bytes.NewBuffer(mem.System, nil)
	if width > 0 {
		fmt.Fprintf(&text, "%s lock · ", event.Kind)
		writeDuration(&text, event.ElapsedMS)
	} else {
		io.WriteString(&text, detail)
	}
	io.WriteString(out, "  ")
	cli.Style(out, "message.warning", status, diagnosticColor == "always")
	io.WriteString(out, "  ")
	if width > 0 {
		writeDashboardField(out, text.String(), width-6-len(status))
	} else {
		io.WriteString(out, text.String())
	}
	io.WriteString(out, "\n")
	text.Free()
}

func writeLockNotice(out io.Writer, event *posix.LockEvent) {
	var text = bytes.NewBuffer(mem.System, nil)
	defer text.Free()
	if event.State == "acquired" {
		io.WriteString(&text, "  ✓ lock acquired after ")
		writeDuration(&text, event.ElapsedMS)
		io.WriteString(&text, "; continuing")
		cli.Style(out, "status.success", text.String(), diagnosticColor == "always")
		io.WriteString(out, "\n")
		return
	}
	io.WriteString(out, "\n")
	writeLockHeading(out, event, 0)
	writeLockRow(out, "lock:", event.Path, "value.path", 0)
	writeLockOwner(&text, event)
	writeLockRow(out, "owner:", text.String(), "value.tool", 0)
	text.Reset()
	if event.State == "waiting" {
		io.WriteString(&text, "waiting ")
		writeDuration(&text, event.ElapsedMS)
	}
	if event.HeldMS >= 0 {
		if text.Len() != 0 {
			io.WriteString(&text, " · ")
		}
		io.WriteString(&text, "held ")
		writeDuration(&text, event.HeldMS)
	}
	if text.Len() != 0 {
		writeLockRow(out, "time:", text.String(), "text.muted", 0)
	}
	if event.State == "waiting" {
		writeLockRow(out, "Ctrl-C", "cancel this invocation; leave the lock in place", "text.muted", 0)
	}
	io.WriteString(out, "\n")
}

func lockUpdateError(out io.Writer) {
	if busyLock.Path == "" {
		cliError(out, "CLEAN_STATE", "cannot safely update output protection or release ownership")
		return
	}
	var message = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&message, "project busy; lock: ")
	writeLockText(&message, busyLock.Path)
	io.WriteString(&message, "; owner: ")
	writeLockOwner(&message, &busyLock)
	cliError(out, "CLEAN_BUSY", message.String())
	message.Free()
}

// Called synchronously inside lock acquisition, before the blocked Tick can
// return. Never leave a misleading "evaluating" dashboard on screen.
func observeLock(event *posix.LockEvent) bool {
	if posix.SignalPending() {
		return false
	}
	first := !lockWaiting
	if event.State == "busy" {
		mem.FreeString(mem.System, busyLock.Path)
		mem.FreeString(mem.System, busyLock.OwnerCommand)
		busyLock = *event
		busyLock.Path = cloneCommandText(event.Path)
		busyLock.OwnerCommand = cloneCommandText(event.OwnerCommand)
	}
	notice := first || event.State != "waiting" || event.ElapsedMS-lockLastNotice >= 5000
	if event.State == "waiting" {
		lockWaiting = true
	} else {
		lockWaiting = false
	}
	if notice {
		lockLastNotice = event.ElapsedMS
	}
	if cliDiagnosticJSON {
		// Static clean keeps its one-document contract; busy details go in it.
		if event.State == "busy" {
			return true
		}
		if notice {
			e := json.NewEncoder(lockOut)
			e.BeginObject()
			e.Str("schema")
			e.Int(1)
			e.Str("type")
			e.Str("lock-" + event.State)
			writeLockFields(&e, event)
			e.EndObject()
			e.Flush()
			io.WriteString(lockOut, "\n")
			flushCLIOutput(lockOut)
		}
		return true
	}
	if dashboardRows != 0 {
		width, height := dashboardDimensions()
		if width != dashboardWidth || height != dashboardHeight {
			clearDashboard(lockErr)
		}
	}
	if notice {
		clearDashboard(lockErr)
		writeLockNotice(lockErr, event)
	}
	if event.State == "waiting" && dashboardCanDraw() {
		var frame = bytes.NewBuffer(mem.System, nil)
		width, height := dashboardDimensions()
		writeLockHeading(&frame, event, width)
		writeLockRow(&frame, "lock:", event.Path, "value.path", width)
		var owner = bytes.NewBuffer(mem.System, nil)
		writeLockOwner(&owner, event)
		writeLockRow(&frame, "owner:", owner.String(), "value.tool", width)
		dashboardWidth, dashboardHeight = width, height
		previous := dashboardRows
		dashboardRows = 3
		paintDashboard(lockErr, frame.String(), previous, 3)
		owner.Free()
		frame.Free()
	}
	flushCLIOutput(lockErr)
	return true
}
