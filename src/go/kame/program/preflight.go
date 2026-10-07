package program

import (
	"kame/core"
	"solod.dev/so/strings"
)

func (p *Program) sourceSignature() core.Signature {
	if p.SourceSignature.Mode == core.SignatureUnavailable {
		var source hashSink
		source.state = newSHA256()
		source.appendText(p.Parsed.Source.Name)
		source.appendText(p.Parsed.Source.Text)
		source.appendText(p.Eval.Script.Source.Name)
		source.appendText(p.Eval.Script.Source.Text)
		p.SourceSignature.Mode = core.SignatureContent
		source.state.Sum(p.SourceSignature.Digest[:])
	}
	return p.SourceSignature
}

func (p *Program) fileGuard(entry *instance) core.Signature {
	var sink hashSink
	sink.state = newSHA256()
	sink.appendText("kame-file-preflight-v1")
	// ponytail: a whole-source guard conservatively falls back to rendering after
	// any code edit; fingerprint reached definitions if edit-time cost warrants it.
	source := p.sourceSignature()
	sink.state.Write(source.Digest[:])
	settings := p.fileImplementation(entry, renderResult{})
	sink.state.Write(settings.Digest[:])
	if entry.Kash {
		sink.appendText("kash")
	} else {
		sink.appendText("shell")
	}
	sink.appendText(p.Options.Directory)
	sink.appendU64(uint64(len(p.Configuration)))
	for i := range p.Configuration {
		sink.appendText(p.Configuration[i])
	}
	count := 0
	for i := range entry.Environment {
		if strings.HasPrefix(entry.Environment[i], "KAME_") {
			count++
		}
	}
	sink.appendU64(uint64(count))
	for i := range entry.Environment {
		if strings.HasPrefix(entry.Environment[i], "KAME_") {
			sink.appendText(entry.Environment[i])
		}
	}
	sink.appendU64(uint64(len(p.Eval.DefinitionArgs)))
	for i := range p.Eval.DefinitionArgs {
		signature := core.ValueSignature(p.Eval.DefinitionArgs[i])
		if signature.Mode == core.SignatureUnavailable {
			return core.Signature{}
		}
		sink.state.Write(signature.Digest[:])
	}
	sink.appendU64(uint64(len(entry.Captures)))
	for i := range entry.Captures {
		sink.appendText(entry.Captures[i].Name)
		sink.appendText(entry.Captures[i].Text)
	}
	sink.appendU64(uint64(len(entry.Plan.Arguments)))
	for i := range entry.Plan.Arguments {
		sink.appendText(entry.Plan.Arguments[i].Name)
		sink.appendText(entry.Plan.Arguments[i].Value)
	}
	sink.appendU64(uint64(len(p.Options.Grants)))
	for i := range p.Options.Grants {
		sink.appendU64(uint64(p.Options.Grants[i].Capability))
		sink.appendU64(uint64(len(p.Options.Grants[i].Names)))
		for j := range p.Options.Grants[i].Names {
			sink.appendText(p.Options.Grants[i].Names[j])
		}
	}
	signature := core.Signature{Mode: core.SignatureContent}
	sink.state.Sum(signature.Digest[:])
	return signature
}

func (p *Program) preflightMiss(c *core.EngineContext, index int) core.ProducerResult {
	entry := &p.Instances[index]
	c.RestoreDependencies(&entry.FileContext.Checkpoint)
	p.freeFileContext(entry.FileContext)
	entry.FileContext = nil
	entry.PreflightChecked = true
	entry.AcceptedRecord.Free(p.Alloc)
	return produce(c, entry.Node.ID)
}

// Reuse engine-owned content identities rather than rereading a file for every
// consumer. Generated multi-output rules expose each physical artifact separately.
func (p *Program) dependencyContentSignature(key core.ResourceKey) core.Signature {
	node := p.Engine.Lookup(key)
	if node == nil || !node.Current {
		return core.Signature{}
	}
	if index := p.instanceIndex(node); index >= 0 {
		for i := range p.Instances[index].AcceptedRecord.Outputs {
			item := p.Instances[index].AcceptedRecord.Outputs[i]
			if item.Key.Name == key.Name {
				return item.Signature
			}
		}
		return core.Signature{}
	}
	return node.Signature
}
