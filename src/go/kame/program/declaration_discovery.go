package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/source"
	"kame/lang/template"
	"path"
	"slices"
	"strings"
)

type declarationDiscovery struct {
	Name    string
	Pattern string
	Span    source.Span
	Origin  int
	Files   core.Value
	Failure diagnostic.Diagnostic
}

// DeclarationSnapshot retains the accepted membership fingerprint, including
// observations from registration that failed after consuming the snapshot.
type DeclarationSnapshot struct {
	Key         core.ResourceKey
	Directory   string
	Fingerprint string
	Pattern     string
	Source      string
	Span        source.Span
}

func (s *DeclarationSnapshot) Free(a mem.Allocator) {
	s.Key.Free(a)
	mem.FreeString(a, s.Directory)
	mem.FreeString(a, s.Fingerprint)
	mem.FreeString(a, s.Pattern)
	mem.FreeString(a, s.Source)
	*s = DeclarationSnapshot{}
}

func (s DeclarationSnapshot) clone(a mem.Allocator) DeclarationSnapshot {
	return DeclarationSnapshot{Key: s.Key.Clone(a), Directory: cloneText(a, s.Directory), Fingerprint: cloneText(a, s.Fingerprint), Pattern: cloneText(a, s.Pattern), Source: cloneText(a, s.Source), Span: s.Span}
}

func (p *Program) declarationSnapshots() []DeclarationSnapshot {
	var snapshots []DeclarationSnapshot
	for _, snapshot := range p.Options.CompositionDiscoveries {
		if !slices.ContainsFunc(snapshots, func(existing DeclarationSnapshot) bool {
			return existing.Key == snapshot.Key && existing.Directory == snapshot.Directory
		}) {
			snapshots = append(snapshots, snapshot.clone(p.Alloc))
		}
	}
	for _, item := range p.declarationSources {
		snapshots = append(snapshots, item.clone(p.Alloc))
	}
	for _, observation := range p.declarationDiscoveries {
		if slices.ContainsFunc(snapshots, func(existing DeclarationSnapshot) bool {
			return existing.Key.Kind == core.ResourceDiscovery && existing.Key.Name == observation.Name && existing.Directory == p.Options.Directory
		}) {
			continue
		}
		fingerprint := cloneText(p.Alloc, "unavailable")
		if observation.Failure.Code == "" {
			mem.FreeString(p.Alloc, fingerprint)
			signature := core.ValueSignature(observation.Files)
			fingerprint = cacheHex(p.Alloc, signature.Digest[:])
		}
		location := p.Eval.LocateSource(p.Parsed.Source.Name, observation.Span)
		if sourceName := p.declarationSourceName(observation.Origin); sourceName != "" {
			location.Source, location.Span = sourceName, observation.Span
		}
		snapshots = append(snapshots, DeclarationSnapshot{
			Key:       core.NewResourceKey(p.Alloc, core.ResourceDiscovery, observation.Name),
			Directory: cloneText(p.Alloc, p.Options.Directory), Fingerprint: fingerprint,
			Pattern: cloneText(p.Alloc, observation.Pattern), Source: cloneText(p.Alloc, location.Source), Span: location.Span,
		})
	}
	return snapshots
}

// DeclarationFingerprint polls the same regular-file membership as registration.
// It is independent of the build graph so watchers can recover a failed load.
func DeclarationFingerprint(a mem.Allocator, filesystem host.ProgramHost, directory string, key core.ResourceKey) string {
	p := Program{Alloc: a, Host: filesystem, Options: Options{Directory: directory}}
	if key.Kind == core.ResourceDeclarationSource {
		result := p.fileCompletion(host.Request{Kind: host.RequestLoadSource}, host.OpRead, key.Name)
		defer result.Value.Free(a)
		defer result.Diagnostic.Free(a)
		return declarationSourceFingerprint(a, result)
	}
	result := p.declarationFiles(key.Name)
	defer result.Value.Free(a)
	defer result.Diagnostic.Free(a)
	if result.Diagnostic.Code != "" {
		return cloneText(a, "unavailable")
	}
	signature := core.ValueSignature(result.Value)
	return cacheHex(a, signature.Digest[:])
}

func observeDeclarationFiles(state any, name, pattern string, span source.Span, origin int) eval.Result {
	p := state.(*generatedEvaluation).Program
	for _, accepted := range p.declarationDiscoveries {
		if accepted.Name == name {
			if accepted.Failure.Code != "" {
				return eval.Result{Diagnostic: p.declarationDiagnostic(span, origin, accepted.Failure.Code, accepted.Failure.Message)}
			}
			return eval.Result{Value: accepted.Files.Clone(p.Alloc)}
		}
	}
	if p.Options.DeclarationRead == nil && (p.Host == nil || p.Forwarding) {
		return eval.Result{Diagnostic: p.declarationDiagnostic(span, origin, "PHASE_INVALID", "declaration discovery requires a planning filesystem snapshot")}
	}
	files := p.declarationFiles(name)
	if !files.HasValue && files.Diagnostic.Code == "" {
		p.registrationPending = true
		return eval.Result{Waiting: true}
	}
	p.declarationDiscoveries = append(p.declarationDiscoveries, declarationDiscovery{
		Name: cloneText(p.Alloc, name), Pattern: cloneText(p.Alloc, pattern), Span: span, Origin: origin, Files: files.Value, Failure: files.Diagnostic,
	})
	if files.Diagnostic.Code != "" {
		return eval.Result{Diagnostic: p.declarationDiagnostic(span, origin, files.Diagnostic.Code, files.Diagnostic.Message)}
	}
	return eval.Result{Value: files.Value.Clone(p.Alloc)}
}

func (p *Program) declarationFiles(name string) core.Completion {
	key, kinds := host.DeclarationDiscovery(name)
	if read := p.Options.DeclarationRead; read != nil {
		result := read(p.Options.DeclarationHost, host.DiscoveryScan(key), kinds)
		if result.Diagnostic.Code != "" || !result.HasValue {
			return result
		}
		return DeclarationMembership(p.Alloc, p.Options.Directory, name, result.Value)
	}
	result := p.wildcard(key)
	if result.Diagnostic.Code != "" {
		return result
	}
	defer result.Value.Free(p.Alloc)
	var paths []core.Value
	defer func() { mem.FreeSlice(nil, paths) }()
	for _, candidate := range result.Value.List {
		if kinds == host.DiscoveryAll {
			candidate.Kind = core.Path
			paths = append(paths, candidate)
			continue
		}
		name := p.canonicalTarget(candidate.Text, true)
		info := p.Host.Lstat(name)
		mem.FreeString(p.Alloc, name)
		if info.Failed {
			return core.Completion{Diagnostic: failure(p.Alloc, "FS_ERR", "cannot inspect discovered path")}
		}
		if info.Exists && kinds&host.DiscoveryKind(info.Info.Regular, info.Info.IsDir, info.Info.Symlink) != 0 {
			candidate.Kind = core.Path
			paths = append(paths, candidate)
		}
	}
	return core.Completion{Value: core.NewList(p.Alloc, paths), HasValue: true}
}

// DeclarationMembership takes a host-owned kind-filtered candidate list and
// returns the exact portable snapshot used by registration and watch.
func DeclarationMembership(a mem.Allocator, directory, name string, candidates core.Value) core.Completion {
	key, _ := host.DeclarationDiscovery(name)
	matches, d := host.FilterDiscovery(a, key, candidates)
	defer matches.Free(a)
	if d.Code != "" {
		return core.Completion{Diagnostic: d}
	}
	if matches.Kind != core.List {
		return core.Completion{Diagnostic: failure(a, "HOST_FAIL", "invalid declaration discovery observation")}
	}
	p := Program{Alloc: a, Options: Options{Directory: directory}}
	var files []core.Value
	defer func() {
		for i := range files {
			files[i].Free(a)
		}
		mem.FreeSlice(nil, files)
	}()
	for _, match := range matches.List {
		if match.Kind != core.String || match.Text == "" || strings.ContainsRune(match.Text, 0) {
			return core.Completion{Diagnostic: failure(a, "HOST_FAIL", "invalid declaration discovery path")}
		}
		absolute := p.canonicalTarget(match.Text, true)
		text := p.relativePath(absolute)
		mem.FreeString(a, absolute)
		files = append(files, core.Value{Kind: core.Path, Text: text})
	}
	slices.SortFunc(files, func(a, b core.Value) int { return strings.Compare(a.Text, b.Text) })
	return core.Completion{Value: core.NewList(a, slices.CompactFunc(files, func(a, b core.Value) bool { return a.Text == b.Text })), HasValue: true}
}

func (p *Program) validateDiscoveryOutputs() diagnostic.Diagnostic {
	for _, snapshot := range p.Options.CompositionDiscoveries {
		discovery := declarationDiscovery{Name: snapshot.Key.Name, Pattern: snapshot.Pattern, Span: snapshot.Span}
		if d := p.validateDiscoveryOutput(discovery); d.Code != "" {
			mem.FreeString(p.Alloc, d.Source)
			d.Source, d.Span = cloneText(p.Alloc, snapshot.Source), diagnostic.Span{Start: snapshot.Span.Start, End: snapshot.Span.End}
			return d
		}
	}
	for _, discovery := range p.declarationDiscoveries {
		if d := p.validateDiscoveryOutput(discovery); d.Code != "" {
			return d
		}
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) validateDiscoveryOutput(discovery declarationDiscovery) diagnostic.Diagnostic {
	for _, registered := range p.Rules {
		if registered.Rule.Kind != rule.FileRule {
			continue
		}
		for _, output := range registered.Rule.Outputs {
			matches, matchError := p.discoveryOverlapsOutput(discovery, output)
			if matchError.Code != "" {
				return matchError
			}
			if matches {
				d := p.declarationDiagnostic(discovery.Span, discovery.Origin, "DISCOVERY_OVERLAP", "discovery pattern overlaps declared output: "+discovery.Pattern)
				location := p.targetLocation(output)
				d.Related = append(d.Related, diagnostic.Related{Message: cloneText(p.Alloc, "output declared here: "+output.Text), Source: cloneText(p.Alloc, location.Source), Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}})
				return d
			}
		}
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) discoveryOverlapsOutput(discovery declarationDiscovery, output rule.Target) (bool, diagnostic.Diagnostic) {
	discovery.Name, _ = host.DeclarationDiscovery(discovery.Name)
	if output.Template {
		return p.discoveryOverlapsPattern(discovery, output)
	}
	name := p.canonicalTarget(output.Text, true)
	defer mem.FreeString(p.Alloc, name)
	if host.DiscoveryScan(discovery.Name) == discovery.Name {
		pattern := p.canonicalTarget(discovery.Name, true)
		defer mem.FreeString(p.Alloc, pattern)
		return inspectionGlobMatch(pattern, name), diagnostic.Diagnostic{}
	}
	if !path.IsAbs(name) && !core.IsResourceURIName(name) {
		name = p.relativePath(name)
		defer mem.FreeString(p.Alloc, name)
	}
	candidates := core.NewList(p.Alloc, []core.Value{{Kind: core.String, Text: name}})
	matches, d := host.FilterDiscovery(p.Alloc, discovery.Name, candidates)
	defer matches.Free(p.Alloc)
	if d.Code != "" {
		defer d.Free(p.Alloc)
		return false, p.declarationDiagnostic(discovery.Span, discovery.Origin, d.Code, d.Message)
	}
	return len(matches.List) != 0, diagnostic.Diagnostic{}
}

func (p *Program) discoveryOverlapsPattern(discovery declarationDiscovery, output rule.Target) (bool, diagnostic.Diagnostic) {
	a := p.Alloc
	outputPattern := &expr.Pattern{Alloc: a}
	defer outputPattern.Free(a)
	for _, part := range output.TargetForm.Parts {
		if part.Kind == template.TargetLiteral {
			outputPattern.Parts = append(outputPattern.Parts, expr.PatternPart{Kind: expr.PatternLiteral, Text: cloneText(a, part.Text)})
		} else {
			outputPattern.Parts = append(outputPattern.Parts, expr.PatternPart{Kind: expr.PatternMatcher, Text: cloneText(a, part.Name), Pattern: cloneText(a, part.Pattern), Regex: part.Regex != nil})
		}
	}
	parsed := host.ParseDiscoveryPattern(a, discovery.Name)
	defer parsed.Pattern.Free(a)
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern != nil && parsed.Pattern.References != 0 {
		return false, p.declarationDiagnostic(discovery.Span, discovery.Origin, "PAT_INVALID", "invalid discovery pattern")
	}
	scan := p.canonicalTarget(host.DiscoveryScan(discovery.Name), true)
	defer mem.FreeString(a, scan)
	outputURI := core.IsResourceURIName(output.Text)
	if core.IsResourceURIName(scan) != outputURI {
		return false, diagnostic.Diagnostic{}
	}
	if outputURI {
		// A URI's scheme separator is structural, never a directory component.
		// Keep the scheme in the language and remove only its extra slash.
		scan = strings.Replace(scan, "://", ":/", 1)
		for _, pattern := range []*expr.Pattern{parsed.Pattern, outputPattern} {
			if pattern != nil && len(pattern.Parts) != 0 && pattern.Parts[0].Kind == expr.PatternLiteral {
				part := &pattern.Parts[0]
				text := strings.Replace(part.Text, "://", ":/", 1)
				mem.FreeString(a, part.Text)
				part.Text = cloneText(a, text)
			}
		}
	} else {
		expr.NormalizePathPatternRoot(a, outputPattern, p.Options.Directory)
	}
	budget := expr.RegexStepLimit
	matched, limited := expr.PathPatternsOverlap(a, parsed.Pattern, outputPattern, scan, &budget)
	if limited {
		if p.discoveredOutputWitness(discovery, output) {
			return true, diagnostic.Diagnostic{}
		}
		return false, p.declarationDiagnostic(discovery.Span, discovery.Origin, "PAT_LIMIT", "discovery overlap check exceeded its step budget")
	}
	return matched, diagnostic.Diagnostic{}
}

func (p *Program) discoveredOutputWitness(discovery declarationDiscovery, output rule.Target) bool {
	// An observed member can establish overlap even when the symbolic search
	// exhausts its budget. Share one match budget across the observed paths.
	budget := expr.RegexStepLimit
	for _, file := range discovery.Files.List {
		name := file.Text
		absolute := path.IsAbs(output.Text)
		if absolute {
			name = p.canonicalTarget(name, true)
		}
		match := output.TargetForm.MatchTargetBudget(p.Alloc, name, &budget)
		if absolute {
			mem.FreeString(p.Alloc, name)
		}
		if match != nil {
			limited := match.Limited
			match.Free(p.Alloc)
			return !limited
		}
	}
	return false
}
