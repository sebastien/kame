package eval

import (
	"encoding/hex"
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"slices"
	"strings"
)

// This is not a valid environment variable name. Only its digest is persisted.
const ProcessEnvironmentName = "@process-environment"

const processEnvironmentPrefix = ProcessEnvironmentName + "/"
const absentEnvironmentPrefix = "@unit-environment-absent/"
const stageEnvironmentOverrides = "environmentOverrides" // evaluator metadata, omitted from the wire setup

func IsProcessEnvironmentObservation(name string) bool {
	return name == ProcessEnvironmentName || strings.HasPrefix(name, processEnvironmentPrefix)
}

func IsExternalEnvironmentObservation(name string) bool {
	return !IsProcessEnvironmentObservation(name) && !strings.HasPrefix(name, absentEnvironmentPrefix)
}

// EnvironmentObservationSignature revalidates the invocation inputs to a
// process environment. Authored edits are covered by the recipe and the
// tracked inputs used to compute them.
func EnvironmentObservationSignature(a mem.Allocator, name string, environment []string) core.Signature {
	switch {
	case name == ProcessEnvironmentName:
		return ProcessEnvironmentSignature(a, environment)
	case strings.HasPrefix(name, processEnvironmentPrefix):
		digest, masks, ok := strings.Cut(name[len(processEnvironmentPrefix):], "\x00")
		if !ok || len(digest) != 64 || masks == "" {
			return core.Signature{}
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return core.Signature{}
		}
		return ProcessEnvironmentSignature(a, omitEnvironmentAssignments(environment, strings.Split(masks, "\x00")))
	case strings.HasPrefix(name, absentEnvironmentPrefix):
		return core.ValueSignature(core.Value{Kind: core.Nil})
	default:
		for _, assignment := range environment {
			variable, value, ok := strings.Cut(assignment, "=")
			if ok && variable == name {
				return core.ValueSignature(core.Value{Kind: core.String, Text: value})
			}
		}
		return core.ValueSignature(core.Value{Kind: core.Nil})
	}
}

func omitEnvironmentAssignments(environment, masks []string) []string {
	projected := make([]string, 0, len(environment))
	for _, assignment := range environment {
		name, _, _ := strings.Cut(assignment, "=")
		if !slices.Contains(masks, name) {
			projected = append(projected, assignment)
		}
	}
	return projected
}

func (c *Context) observeProcessEnvironment(payload core.Value) {
	setups := host.PayloadSetups(payload)
	if len(setups) == 0 {
		c.observeStageEnvironment(payload, c.environmentOverrides(nil), true)
		return
	}
	kinds := host.PayloadList(payload, "kinds")
	for i, setup := range setups {
		if i < len(kinds) {
			if operator := host.LookupOperatorKind(kinds[i].Text); operator != nil && !operator.Argv {
				continue
			}
		}
		masks := environmentStrings(host.PayloadList(setup, stageEnvironmentOverrides))
		c.observeStageEnvironment(setup, masks, host.PayloadBool(setup, host.FieldEnvironmentExact))
	}
}

func (c *Context) observeStageEnvironment(setup core.Value, masks []string, exact bool) {
	name := ProcessEnvironmentName
	signature := c.Program.processEnvironmentSignature(c.Run, c.Environment)
	if len(masks) != 0 {
		assignments := environmentStrings(host.PayloadList(setup, host.FieldEnvironment))
		environment := assignments
		if !exact {
			environment = slices.Clone(c.Environment)
			for _, assignment := range assignments {
				environment = replaceEnvironmentAssignment(environment, assignment)
			}
		}
		effective := ProcessEnvironmentSignature(c.Run, environment)
		// Keep the effective child digest in the proof identity. The
		// revalidated signature covers the inherited values it still uses.
		name = processEnvironmentPrefix + hex.EncodeToString(effective.Digest[:]) + "\x00" + strings.Join(masks, "\x00")
		signature = ProcessEnvironmentSignature(c.Run, omitEnvironmentAssignments(c.Environment, masks))
	}
	c.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: name}, core.ObservationValue, signature)
}

func environmentStrings(values []core.Value) []string {
	if len(values) == 0 {
		return nil
	}
	texts := make([]string, len(values))
	for i := range values {
		texts[i] = values[i].Text
	}
	return texts
}

// ProcessEnvironmentSignature ignores assignment order, not child environment values.
func ProcessEnvironmentSignature(a mem.Allocator, environment []string) core.Signature {
	sorted := slices.Clone(environment)
	slices.Sort(sorted)
	values := make([]core.Value, len(sorted))
	for i := range sorted {
		values[i] = core.Value{Kind: core.String, Text: sorted[i]}
	}
	mem.FreeSlice(nil, sorted)
	signature := core.ValueSignature(core.Value{Kind: core.List, List: values})
	mem.FreeSlice(nil, values)
	return signature
}

// processEnvironmentSignature memoizes ProcessEnvironmentSignature for the
// environment slice most recently presented: contexts share that slice and
// never mutate it in place, and every process request observes it.
func (p *Program) processEnvironmentSignature(a mem.Allocator, environment []string) core.Signature {
	if p == nil {
		return ProcessEnvironmentSignature(a, environment)
	}
	if len(environment) != 0 && p.processEnvironmentLen == len(environment) && p.processEnvironmentBase == &environment[0] {
		return p.processEnvironmentValue
	}
	signature := ProcessEnvironmentSignature(a, environment)
	if len(environment) != 0 {
		p.processEnvironmentBase, p.processEnvironmentLen, p.processEnvironmentValue = &environment[0], len(environment), signature
	}
	return signature
}
