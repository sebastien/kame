package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) prepareRemoteExecution(entry *instance) diagnostic.Diagnostic {
	for i := range entry.ExecutionInputs { entry.ExecutionInputs[i].Free(p.Alloc) }
	slices.Free(p.Alloc, entry.ExecutionInputs)
	entry.ExecutionInputs = nil
	for i := range entry.ExecutionOutputs { mem.FreeString(p.Alloc, entry.ExecutionOutputs[i]) }
	slices.Free(p.Alloc, entry.ExecutionOutputs)
	entry.ExecutionOutputs = nil
	if entry.Executor == "" || entry.Executor == "local" { return diagnostic.Diagnostic{} }
	if entry.Rule.Kind != rule.FileRule { return failure(p.Alloc, "FEATURE_UNSUP", "remote execution is supported only for file rules") }
	if p.Forwarding { return failure(p.Alloc, "FEATURE_UNSUP", "remote executors require a process host adapter") }
	if p.Host == nil { return failure(p.Alloc, "FEATURE_UNSUP", "remote executor host is unavailable") }
	if !p.Host.SupportsExecutor(entry.Executor, entry.ExecutorVersion) {
		return failure(p.Alloc, "FEATURE_UNSUP", "remote executor is unavailable: "+entry.Executor)
	}
	inputs := entry.Plan.ResolvedInputs
	if !entry.Plan.Resolved { inputs = entry.Plan.Inputs }
	for i := range inputs {
		if !isFileName(inputs[i]) { continue }
		name := p.canonicalTarget(inputs[i], true)
		stat := p.Host.Stat(name)
		if !stat.Exists || !stat.Info.Regular {
			mem.FreeString(p.Alloc, name)
			for j := range entry.ExecutionInputs { entry.ExecutionInputs[j].Free(p.Alloc) }
			slices.Free(p.Alloc, entry.ExecutionInputs)
			entry.ExecutionInputs = nil
			return failure(p.Alloc, "FS_ERR", "remote input is missing or not a regular file")
		}
		data, err := p.Host.ReadFile(p.Alloc, name)
		if err != nil {
			mem.FreeString(p.Alloc, name)
			for j := range entry.ExecutionInputs { entry.ExecutionInputs[j].Free(p.Alloc) }
			slices.Free(p.Alloc, entry.ExecutionInputs)
			entry.ExecutionInputs = nil
			return failure(p.Alloc, "FS_ERR", "cannot read remote input")
		}
		artifact := host.ExecutionArtifact{Name: remoteArtifactName(p, name), Data: data, Mode: stat.Info.Mode}
		cacheHash(data, artifact.Digest[:])
		entry.ExecutionInputs = slices.Append(p.Alloc, entry.ExecutionInputs, artifact)
		mem.FreeString(p.Alloc, name)
	}
	for i := range entry.Plan.Outputs {
		canonical := p.canonicalTarget(entry.Plan.Outputs[i], true)
		entry.ExecutionOutputs = slices.Append(p.Alloc, entry.ExecutionOutputs, remoteArtifactName(p, canonical))
		mem.FreeString(p.Alloc, canonical)
	}
	key := hashSink{state: newSHA256()}
	key.appendText("kame-remote-idempotency-v1")
	key.appendText(entry.Plan.Target)
	key.appendText(entry.Executor)
	key.appendText(entry.ExecutorVersion)
	key.appendText(entry.Script)
	for i := range entry.ExecutionInputs {
		key.appendText(entry.ExecutionInputs[i].Name)
		key.state.Write(entry.ExecutionInputs[i].Digest[:])
	}
	key.state.Sum(entry.ExecutionKey[:])
	return diagnostic.Diagnostic{}
}

func remoteArtifactName(p *Program, canonical string) string {
	if core.IsResourceURIName(canonical) { return cloneText(p.Alloc, canonical) }
	directory := p.canonicalTarget(".", true)
	prefix := directory
	if len(prefix) == 0 || prefix[len(prefix)-1] != '/' { prefix += "/" }
	if strings.HasPrefix(canonical, prefix) {
		name := cloneText(p.Alloc, canonical[len(prefix):])
		mem.FreeString(p.Alloc, directory)
		return name
	}
	mem.FreeString(p.Alloc, directory)
	return cloneText(p.Alloc, canonical)
}

func (p *Program) releaseRemoteExecution(entry *instance) {
	if entry == nil { return }
	for i := range entry.ExecutionInputs { entry.ExecutionInputs[i].Free(p.Alloc) }
	slices.Free(p.Alloc, entry.ExecutionInputs)
	entry.ExecutionInputs = nil
	for i := range entry.ExecutionOutputs { mem.FreeString(p.Alloc, entry.ExecutionOutputs[i]) }
	slices.Free(p.Alloc, entry.ExecutionOutputs)
	entry.ExecutionOutputs = nil
}

func (p *Program) publishRemoteOutputs(entry *instance, outputs []host.ExecutionArtifact) diagnostic.Diagnostic {
	if entry == nil || entry.Executor == "" || entry.Executor == "local" { return diagnostic.Diagnostic{} }
	seen := slices.Make[bool](p.Alloc, len(entry.ExecutionOutputs))
	for i := range outputs {
		matched := -1
		for j := range entry.ExecutionOutputs {
			if outputs[i].Name == entry.ExecutionOutputs[j] { matched = j; break }
		}
		if matched < 0 || seen[matched] {
			slices.Free(p.Alloc, seen)
			return failure(p.Alloc, "HOST_FAIL", "remote executor returned an undeclared or duplicate output")
		}
		seen[matched] = true
	}
	for i := range seen {
		if !seen[i] {
			slices.Free(p.Alloc, seen)
			return failure(p.Alloc, "OUTPUT_MISSING", "remote executor omitted a declared output")
		}
	}
	slices.Free(p.Alloc, seen)
	for i := range outputs {
		outputPath := p.canonicalTarget(entry.Plan.Outputs[0], true)
		for j := range entry.ExecutionOutputs { if entry.ExecutionOutputs[j] == outputs[i].Name { mem.FreeString(p.Alloc, outputPath); outputPath = p.canonicalTarget(entry.Plan.Outputs[j], true); break } }
		mode := outputs[i].Mode & 0o777
		if !p.mkdirParent(outputPath) || p.Host.WriteFileAtomic(outputPath, outputs[i].Data, mode, false) != nil {
			mem.FreeString(p.Alloc, outputPath)
			return failure(p.Alloc, "FS_ERR", "cannot publish remote output")
		}
		mem.FreeString(p.Alloc, outputPath)
	}
	return diagnostic.Diagnostic{}
}
