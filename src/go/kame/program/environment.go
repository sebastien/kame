package program

import (
	"kame/lang/eval"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// claimEnvironment binds a recipe and its prerequisite closure to one effective
// environment. Rule-local values override inherited values; last assignment wins.
// Active shared resources cannot be rebound to a conflicting context.
func (p *Program) claimEnvironment(index int, inherited []string) bool {
	d := p.recipeSettings(index)
	p.Instances[index].SettingsDiagnostic.Free(p.Alloc)
	p.Instances[index].SettingsDiagnostic = d
	if d.Code != "" { return false }
	entry := &p.Instances[index]
	// The common inherited snapshot is already canonical and owned. Keep it
	// instead of reconstructing it on every dependency-validation attempt.
	if entry.EnvironmentClaimed && len(entry.Rule.Environment) == 0 && len(entry.MetadataEnvironment) == 0 && sameEnvironment(entry.Environment, inherited) {
		return true
	}
	var environment []string
	for i := range inherited {
		environment = p.setEnvironment(environment, inherited[i])
	}
	for i := range p.Instances[index].Rule.Environment {
		environment = p.setEnvironment(environment, p.Instances[index].Rule.Environment[i].Value)
	}
	for i := range p.Instances[index].MetadataEnvironment { environment = p.setEnvironment(environment, p.Instances[index].MetadataEnvironment[i]) }
	// Canonical order prevents equivalent inherited assignments from creating
	// different cache fingerprints merely through authored ordering.
	for i := 1; i < len(environment); i++ {
		value := environment[i]
		j := i
		for j > 0 && environment[j-1] > value {
			environment[j] = environment[j-1]
			j--
		}
		environment[j] = value
	}
	entry = &p.Instances[index]
	if entry.EnvironmentClaimed {
		// Suspended Kash contexts borrow this snapshot; an equal claim must keep it alive.
		if sameEnvironment(entry.Environment, environment) {
			slices.Free(p.Alloc, environment)
			return true
		}
		if entry.Node.Interest > 0 {
			slices.Free(p.Alloc, environment)
			return false
		}
		p.Engine.Invalidate(entry.Node)
	}
	resolved := cloneStrings(p.Alloc, environment)
	slices.Free(p.Alloc, environment)
	freeStrings(p.Alloc, entry.Environment)
	entry.Environment = resolved
	entry.EnvironmentClaimed = true
	entry.ScopedEnvironment = !sameEnvironment(resolved, p.Options.Environment)
	return true
}

func (p *Program) setEnvironment(environment []string, assignment string) []string {
	equal := strings.IndexByte(assignment, '=')
	if equal < 0 {
		return slices.Append(p.Alloc, environment, assignment)
	}
	name := assignment[:equal]
	for i := range environment {
		at := strings.IndexByte(environment[i], '=')
		if at >= 0 && environment[i][:at] == name {
			environment[i] = assignment
			return environment
		}
	}
	return slices.Append(p.Alloc, environment, assignment)
}

func sameEnvironment(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	ordered := true
	for i := range left {
		if left[i] != right[i] { ordered = false; break }
	}
	if ordered { return true }
	for i := range left {
		found := false
		for j := range right {
			if left[i] == right[j] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Bind lazy definition identity to the same snapshot as recipe evaluation.
// The digest contains no plaintext environment values in graph/event keys.
func (p *Program) bindDefinitionEnvironment(context *eval.Context) {
    _ = p
    if !context.HasEnvironment { return }
    var identity hashSink
    identity.state = newSHA256()
    identity.appendText("kame-definition-environment-v1")
    identity.appendText(context.Cwd)
     identity.appendU64(uint64(context.Phase))
     for i := range context.RuleFrames { if context.RuleFrames[i].ServiceRule { identity.appendText("service-no-file-outputs") } }
    for i := range context.Environment { identity.appendText(context.Environment[i]) }
    identity.state.Sum(context.DefinitionNamespace[:])
    context.HasDefinitionNamespace = true
}
