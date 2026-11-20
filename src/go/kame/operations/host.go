package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"path"
	"slices"
	"strconv"
	"strings"
)

func request(c *eval.Context, kind host.RequestKind, payload core.Value) eval.Result {
	if c.Engine == nil && c.Requests == nil {
		// Hostless synchronous evaluators surface the boundary without queueing work.
		payload.Free(c.Run)
		return eval.Result{Waiting: true}
	}
	if c.Engine != nil && c.Program != nil && (kind == host.RequestReadFile || kind == host.RequestEnvironment) {
		return readRequest(c, kind, payload)
	}
	if kind != host.RequestReadFile && kind != host.RequestEnvironment {
		c.UnvalidatedRead()
	}
	completion := c.TakeCompletion()
	if completion.RequestID != 0 {
		// Resume does not submit, so the freshly built payload is still owned here.
		payload.Free(c.Run)
		if completion.Diagnostic.Code != "" {
			result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			return result
		}
		if !completion.HasValue {
			return c.InvalidOperation("host completion omitted its result")
		}
		result := eval.Result{Value: completion.Value.Clone(c.Run)}
		completion.Value.Free(c.Run)
		return result
	}
	id := c.Submit(kind, payload)
	payload.Free(c.Run)
	if id == 0 {
		return failure("HOST_FAIL", "host request was not accepted")
	}
	return eval.Result{Waiting: true}
}

// Read-only requests are stable within an engine generation. Retain their
// values so replaying an outer expression neither repeats a completed read nor
// lets an earlier call consume a later call's completion.
type readRequestState struct {
	Alloc           mem.Allocator
	Kind            host.RequestKind
	Name            string
	Op              string
	ID              int64
	Value           core.Value
	Signature       core.Signature
	Done            bool
	Diagnostic      diagnostic.Diagnostic
	CheckingMissing bool
	Deferred        bool
}

func freeReadRequestState(a mem.Allocator, value any) {
	_ = a
	state := value.(*readRequestState)
	mem.FreeString(state.Alloc, state.Name)
	mem.FreeString(state.Alloc, state.Op)
	state.Value.Free(state.Alloc)
	state.Diagnostic.Free(state.Alloc)
	mem.Free(state.Alloc, state)
}

func readRequest(c *eval.Context, kind host.RequestKind, payload core.Value) eval.Result {
	name, op := host.PayloadPath(payload), host.PayloadText(payload, host.FieldOp)
	if discovery := host.PayloadText(payload, host.FieldDiscoveryKey); op == host.OpWildcard && discovery != "" {
		name = discovery
	}
	if kind == host.RequestEnvironment {
		name = payload.Text
	}
	var state *readRequestState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*readRequestState)
		if state.Kind != kind || state.Name != name || state.Op != op {
			c.ClearOperationState()
			state = nil
		}
	}
	if state == nil {
		state = mem.Alloc[readRequestState](c.Program.Alloc)
		state.Alloc, state.Kind = c.Program.Alloc, kind
		state.Name, state.Op = core.NewString(state.Alloc, name).Text, core.NewString(state.Alloc, op).Text
		c.SetOperationState(state, freeReadRequestState)
	}
	if state.Done {
		payload.Free(c.Run)
		return readRequestValue(c, state)
	}
	if state.ID != 0 {
		payload.Free(c.Run)
		if c.Completion().RequestID != state.ID {
			return eval.Result{Waiting: true}
		}
		completion := c.TakeCompletion()
		if state.CheckingMissing {
			state.Deferred = completion.Diagnostic.Code == "" && completion.HasValue && completion.Value.Kind == core.Nil
			state.Done = true
			completion.Diagnostic.Free(c.Run)
			completion.Value.Free(c.Run)
			return readRequestValue(c, state)
		}
		if completion.Diagnostic.Code != "" {
			if completion.Diagnostic.Code == "FS_ERR" && state.Op == host.OpRead && c.Program.DryRun && c.Program.GeneratedFile != nil && c.Program.GeneratedFile(c.Program.GeneratedFileState, state.Name) {
				// A failed read alone does not prove absence (permissions, links,
				// etc.). Use the same content probe as read-only inspection.
				state.Diagnostic = completion.Diagnostic.Clone(state.Alloc)
				completion.Diagnostic.Free(c.Run)
				completion.Value.Free(c.Run)
				probe := host.FilePayload(c.Run, host.OpFileContent, state.Name)
				state.ID = c.Submit(host.RequestReadFile, probe)
				probe.Free(c.Run)
				if state.ID == 0 {
					return failure("HOST_FAIL", "host request was not accepted")
				}
				state.CheckingMissing = true
				return eval.Result{Waiting: true}
			}
			observeReadRequest(c, state, core.Signature{})
			result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			completion.Value.Free(c.Run)
			return result
		}
		if !completion.HasValue {
			completion.Value.Free(c.Run)
			observeReadRequest(c, state, core.Signature{})
			return c.InvalidOperation("host completion omitted its result")
		}
		if state.Op == host.OpWildcard {
			var d diagnostic.Diagnostic
			completion.Value, d = host.FilterDiscovery(c.Run, state.Name, completion.Value)
			if d.Code != "" {
				state.Done, state.Diagnostic = true, d
				observeReadRequest(c, state, core.Signature{})
				return readRequestValue(c, state)
			}
		}
		state.Value, state.Done = completion.Value.Clone(state.Alloc), true
		if state.Kind == host.RequestReadFile && state.Op == host.OpRead {
			if state.Value.Kind == core.Bytes {
				state.Signature = core.ContentSignature(state.Value.Bytes)
			}
		} else if state.Op == host.OpStat || state.Op == host.OpLstat {
			state.Signature = core.MetadataSignature(state.Value)
		} else {
			state.Signature = core.ValueSignature(state.Value)
		}
		completion.Value.Free(c.Run)
		return readRequestValue(c, state)
	}
	state.ID = c.Submit(kind, payload)
	payload.Free(c.Run)
	if state.ID == 0 {
		return failure("HOST_FAIL", "host request was not accepted")
	}
	return eval.Result{Waiting: true}
}

func readRequestValue(c *eval.Context, state *readRequestState) eval.Result {
	if state.Deferred {
		return failure("READ_DEFERRED", "generated bytes unavailable during dry-run")
	}
	if state.Diagnostic.Code != "" {
		return eval.Result{Diagnostic: state.Diagnostic.Clone(c.Run)}
	}
	observeReadRequest(c, state, state.Signature)
	return eval.Result{Value: state.Value.Clone(c.Run)}
}

func observeReadRequest(c *eval.Context, state *readRequestState, signature core.Signature) {
	key := core.ResourceKey{Kind: core.ResourceFile, Name: state.Name}
	aspect := core.ObservationContent
	if state.Kind == host.RequestEnvironment {
		key.Kind, aspect = core.ResourceEnvironment, core.ObservationValue
	} else if state.Op == host.OpExists {
		aspect = core.ObservationExistence
	} else if state.Op == host.OpStat {
		aspect = core.ObservationMetadata
	} else if state.Op == host.OpLstat {
		aspect = core.ObservationLinkMetadata
	} else if state.Op == host.OpReadlink {
		aspect = core.ObservationLinkTarget
	} else if state.Op == host.OpWildcard {
		key.Kind, aspect = core.ResourceGlob, core.ObservationValue
	}
	c.Observe(key, aspect, signature)
}

func dependency(c *eval.Context, kind core.ResourceKind, name string) bool {
	key := core.NewResourceKey(c.Run, kind, name)
	current := c.Dependency(key)
	key.Free(c.Run)
	return current
}
func fileRequest(c *eval.Context, op string, value core.Value) eval.Result {
	name := ""
	grouped := false
	if op == host.OpWildcard {
		if value.Kind != core.PathPattern {
			c.DiscardCallables(&value)
			return c.InvalidArgument(0, "path pattern", value.Kind)
		}
		glob, hasGroups, ok := wildcardPatternText(c.Run, value)
		if !ok || glob == "" {
			return ownedFailure(c.Run, "PAT_INVALID", "invalid discovery pattern")
		}
		defer mem.FreeString(c.Run, glob)
		name, grouped = glob, hasGroups
	} else if value.Kind == core.Resource && value.Resource.Kind == core.ResourceFile {
		name = value.Resource.Name
	} else if value.IsPathText() {
		name = value.Text
	} else {
		c.DiscardCallables(&value)
		return c.InvalidArgument(0, "string or file resource", value.Kind)
	}
	// Scoped absolute cwd must reach the host as well as the graph edge. Keep
	// boundary-relative logical names (including glob identities) host-owned.
	resolved := ""
	if path.IsAbs(c.Cwd) && (c.Program == nil || c.Cwd != c.Program.DefinitionCwd) {
		resolved = c.CanonicalPath(name)
		name = resolved
	}
	defer mem.FreeString(c.Run, resolved)
	allowed := c.Allows(eval.Read, name)
	if op == host.OpWildcard {
		allowed = allowsWildcard(c, name)
	}
	if !allowed {
		return failure("CAP_DENIED", "read access denied; grant the required path with --allow-read=ROOT")
	}
	kind := core.ResourceFile
	if op == host.OpWildcard {
		kind = core.ResourceGlob
	}
	if grouped {
		root := c.Cwd
		if c.Program != nil {
			root = c.Program.DefinitionCwd
		}
		name = host.DiscoveryKey(c.Run, name, value.Text, c.Cwd, root)
		defer mem.FreeString(c.Run, name)
	}
	if !c.DirectHostRequests && !dependency(c, kind, name) {
		return eval.Result{Waiting: true}
	}
	result := request(c, host.RequestReadFile, host.FilePayload(c.Run, op, name))
	if grouped && (c.Engine == nil || c.Program == nil) && !result.Waiting && result.Diagnostic.Code == "" {
		result.Value, result.Diagnostic = host.FilterDiscovery(c.Run, name, result.Value)
	}
	if op == host.OpWildcard && !result.Waiting && result.Diagnostic.Code == "" {
		if !discoveryPaths(c, &result.Value) {
			result.Value.Free(c.Run)
			result = failure("HOST_FAIL", "wildcard observation must return a list of paths")
		}
	}
	if result.Diagnostic.Code != "" {
		if !result.Diagnostic.Owned {
			owned := result.Diagnostic.Clone(c.Run)
			result.Diagnostic.Free(c.Run)
			result.Diagnostic = owned
		}
		// Keep the failing resource identity, not file contents. Inspection can
		// then distinguish proven generated absence from an unrelated read error.
		label := core.NewString(c.Run, host.DiscoveryPattern(name))
		kind := core.NewString(c.Run, "resource")
		result.Diagnostic.Frames = append(result.Diagnostic.Frames, diagnostic.Frame{Kind: kind.Text, Label: label.Text})
	}
	return result
}

// The host wire carries path names as strings. Expose typed values without
// changing the host protocol or mutating shared observation storage.
func discoveryPaths(c *eval.Context, value *core.Value) bool {
	if value.Kind != core.List {
		return false
	}
	for _, member := range value.List {
		if !member.IsPathText() {
			return false
		}
	}
	value.Unshare(c.Run)
	for i := range value.List {
		value.List[i].Kind = core.Path
	}
	return true
}

// Check the literal search root, not the escaped glob spelling or its matches.
func allowsWildcard(c *eval.Context, pattern string) bool {
	b := strings.Builder{}
	defer b.Reset()
	rootEnd := -1
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			i++
			b.WriteByte(pattern[i])
		} else {
			if rootEnd < 0 && (ch == '*' || ch == '?' || ch == '[') {
				rootEnd = len(b.String())
			}
			b.WriteByte(ch)
		}
	}
	decoded := b.String()
	if !c.Allows(eval.Read, decoded) {
		return false
	}
	if rootEnd < 0 {
		return true
	}
	root := "."
	if slash := strings.LastIndexByte(decoded[:rootEnd], '/'); slash >= 0 {
		root = decoded[:slash+1]
	}
	return c.Allows(eval.Read, root)
}
func opRead(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpRead, v[0])
}
func opExists(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpExists, v[0])
}
func opStat(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpStat, v[0])
}
func opLstat(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpLstat, v[0])
}
func opReadlink(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return fileRequest(c, host.OpReadlink, v[0])
}

// Reject authored invalid flags before argument glob expressions can issue reads.
func wildcardPreflight(c *eval.Context, arguments []*expr.Expr) eval.Result {
	positive, negative := 0, 0
	for i := range arguments {
		e := arguments[i]
		text := ""
		if e.Kind == expr.Symbol {
			text = e.Text
		}
		if e.Kind == expr.String && e.Pattern == nil && len(e.Parts) == 1 && e.Parts[0].Expr == nil {
			text = e.Parts[0].Text
		}
		if len(text) == 0 || (text[0] != '+' && text[0] != '-') {
			continue
		}
		bit := wildcardKindFlag(text[1:])
		if bit == 0 {
			return c.InvalidOperation("unknown wildcard kind flag: " + text)
		}
		if text[0] == '+' {
			positive |= bit
		} else {
			negative |= bit
		}
		if positive&negative != 0 {
			return c.InvalidOperation("opposite wildcard kind flags are invalid")
		}
	}
	return eval.Result{}
}

func opFiles(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return wildcardShorthand(c, v, "files", host.DiscoveryFile)
}

func opDirs(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return wildcardShorthand(c, v, "dirs", host.DiscoveryDirectory)
}

// files and dirs are wildcard with a fixed entry kind. Extra kind flags stay
// on wildcard, where unions and exclusions are explicit.
func wildcardShorthand(c *eval.Context, v []core.Value, name string, kind int) eval.Result {
	for i := range v {
		value := v[i]
		if (value.Kind == core.Symbol || value.Kind == core.String) && len(value.Text) != 0 && (value.Text[0] == '+' || value.Text[0] == '-') {
			discardArgCallables(c, v)
			return c.InvalidOperation(name + " does not accept kind flags; use wildcard")
		}
	}
	return wildcard(c, v, kind)
}

func kindShorthandPreflight(c *eval.Context, arguments []*expr.Expr) eval.Result {
	for i := range arguments {
		e := arguments[i]
		text := ""
		if e.Kind == expr.Symbol {
			text = e.Text
		}
		if e.Kind == expr.String && e.Pattern == nil && len(e.Parts) == 1 && e.Parts[0].Expr == nil {
			text = e.Parts[0].Text
		}
		if len(text) != 0 && (text[0] == '+' || text[0] == '-') {
			return c.InvalidOperation("kind shorthand does not accept kind flags; use wildcard")
		}
	}
	return eval.Result{}
}

func opWildcard(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return wildcard(c, v, 0)
}

func wildcard(c *eval.Context, v []core.Value, kind int) eval.Result {
	declaration := c.Phase == eval.PlanningPhase && c.DeclarationFiles != nil
	var patterns []core.Value
	var positions []int
	defer func() {
		mem.FreeSlice(nil, patterns)
		mem.FreeSlice(nil, positions)
	}()
	positive, negative := kind, 0
	for i := range v {
		value := v[i]
		if (value.Kind == core.Symbol || value.Kind == core.String) && len(value.Text) != 0 && (value.Text[0] == '+' || value.Text[0] == '-') {
			bit := wildcardKindFlag(value.Text[1:])
			if bit == 0 {
				discardArgCallables(c, v)
				return c.InvalidOperation("unknown wildcard kind flag: " + value.Text)
			}
			if value.Text[0] == '+' {
				positive |= bit
			} else {
				negative |= bit
			}
			if positive&negative != 0 {
				discardArgCallables(c, v)
				return c.InvalidOperation("opposite wildcard kind flags are invalid")
			}
		} else {
			if value.Kind == core.List {
				for j := range value.List {
					if value.List[j].Kind != core.Path {
						return invalidArgument(c, v, i, "flat list of paths")
					}
				}
			} else if value.Kind != core.PathPattern {
				return invalidArgument(c, v, i, "path pattern or list of paths")
			}
			patterns = append(patterns, value)
			if declaration {
				positions = append(positions, i)
			}
		}
	}
	if len(patterns) == 0 {
		return c.InvalidOperation("wildcard requires at least one pattern")
	}
	mask := positive
	if mask == 0 {
		mask = host.DiscoveryAll
	}
	mask &= ^negative
	if declaration {
		return declarationWildcard(c, patterns, positions, mask)
	}
	previous := c.CallPath

	identity := core.NewString(c.Run, previous+"/wildcard-mask:"+strconv.Itoa(mask))
	c.CallPath = identity.Text
	result := eval.Result{}
	// The filter owns the union while suspended; do not rebuild it on replay.
	if (positive != 0 || negative != 0) && c.OperationState() != nil {
		result = filterWildcardKinds(c, core.Value{}, mask)
	} else {
		unionPath := core.NewString(c.Run, c.CallPath+"/wildcard-union")
		c.CallPath = unionPath.Text
		result = wildcardUnion(c, patterns)
		c.CallPath = identity.Text
		unionPath.Free(c.Run)
		if !result.Waiting && result.Diagnostic.Code == "" && (positive != 0 || negative != 0) {
			result = filterWildcardKinds(c, result.Value, mask)
		}
	}
	c.CallPath = previous
	identity.Free(c.Run)
	return result
}

func wildcardKindFlag(name string) int {
	if name == "files" {
		return host.DiscoveryFile
	}
	if name == "dir" {
		return host.DiscoveryDirectory
	}
	if name == "symlinks" {
		return host.DiscoveryLink
	}
	return 0
}

func declarationWildcard(c *eval.Context, operands []core.Value, positions []int, kinds int) eval.Result {
	var paths []core.Value
	defer func() { freeValues(c, paths) }()
	for i, operand := range operands {
		if operand.Kind != core.List {
			observed := observeDeclarationPattern(c, operand, positions[i], kinds)
			if observed.Waiting || observed.Diagnostic.Code != "" {
				return observed
			}
			for _, value := range observed.Value.List {
				paths = append(paths, value.Clone(c.Run))
			}
			observed.Value.Free(c.Run)
			continue
		}
		// An already discovered path is literal. A kind filter observes that
		// exact path; no component can become a glob in the second operation.
		for _, value := range operand.List {
			if kinds != host.DiscoveryAll {
				observed := observeDeclarationPattern(c, value, positions[i], kinds)
				if observed.Waiting || observed.Diagnostic.Code != "" {
					return observed
				}
				matches := len(observed.Value.List) != 0
				observed.Value.Free(c.Run)
				if !matches {
					continue
				}
			}
			paths = append(paths, value.Clone(c.Run))
		}
	}
	paths = sortedWildcardPaths(c.Run, paths)
	return eval.Result{Value: core.NewList(c.Run, paths)}
}

func observeDeclarationPattern(c *eval.Context, value core.Value, position, kinds int) eval.Result {
	var scan string
	grouped := false
	if value.Kind == core.PathPattern {
		var ok bool
		scan, grouped, ok = wildcardPatternText(c.Run, value)
		if !ok || scan == "" {
			mem.FreeString(c.Run, scan)
			return ownedFailure(c.Run, "PAT_INVALID", "invalid discovery pattern")
		}
	} else {
		var escaped strings.Builder
		for i := range len(value.Text) {
			b := value.Text[i]
			if strings.ContainsRune("\\*?[]", rune(b)) {
				escaped.WriteByte('\\')
			}
			escaped.WriteByte(b)
		}
		scan = core.NewString(c.Run, escaped.String()).Text
		escaped.Reset()
	}
	defer mem.FreeString(c.Run, scan)
	if !allowsWildcard(c, scan) {
		return failure("CAP_DENIED", "read access denied; grant the required path with --allow-read=ROOT")
	}
	key := scan
	if grouped {
		key = host.DiscoveryKey(c.Run, scan, value.Text, c.Cwd, c.Cwd)
		defer mem.FreeString(c.Run, key)
	}
	observation := host.DeclarationDiscoveryKey(c.Run, key, kinds)
	defer mem.FreeString(c.Run, observation)
	return c.DeclarationFiles(c.ResolverState, observation, value.Text, c.ArgumentSpan(position), c.SourceOrigin)
}

type wildcardState struct {
	Alloc   mem.Allocator
	Matches core.Value
	Values  []core.Value
	Index   int
	Tracked bool
}

func freeWildcardState(a mem.Allocator, value any) {
	_ = a
	state := value.(*wildcardState)
	state.Matches.Free(state.Alloc)
	for i := range state.Values {
		state.Values[i].Free(state.Alloc)
	}
	mem.FreeSlice(nil, state.Values)
	mem.Free(state.Alloc, state)
}

func discardWildcard(c *eval.Context, state *wildcardState) {
	if state.Tracked {
		c.ClearOperationState()
	} else {
		freeWildcardState(c.Run, state)
	}
}

func wildcardProgress(c *eval.Context, matches core.Value) *wildcardState {
	var state *wildcardState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*wildcardState)
	}
	if state == nil {
		state = mem.Alloc[wildcardState](c.Run)
		state.Alloc, state.Matches = c.Run, matches
		if c.Engine != nil && c.Program != nil {
			state.Tracked = true
			c.SetOperationState(state, freeWildcardState)
		}
	}
	return state
}

func filterWildcardKinds(c *eval.Context, matches core.Value, mask int) eval.Result {
	state := wildcardProgress(c, matches)
	for state.Index < len(state.Matches.List) {
		i := state.Index
		previous := c.CallPath

		identity := core.NewString(c.Run, previous+"/wildcard-kind:"+strconv.Itoa(i))
		c.CallPath = identity.Text
		result := fileRequest(c, host.OpLstat, state.Matches.List[i])
		c.CallPath = previous
		identity.Free(c.Run)
		if result.Waiting {
			if !state.Tracked {
				discardWildcard(c, state)
			}
			return result
		}
		if result.Diagnostic.Code != "" {
			discardWildcard(c, state)
			return result
		}
		regular, directory, link := false, false, false
		fields := 0
		for j := range result.Value.Record {
			field := result.Value.Record[j]
			if field.Value.Kind != core.Bool {
				continue
			}
			if field.Key == "regular" {
				regular = field.Value.Bool
				fields++
			}
			if field.Key == "dir" {
				directory = field.Value.Bool
				fields++
			}
			if field.Key == "link" {
				link = field.Value.Bool
				fields++
			}
		}
		result.Value.Free(c.Run)
		if fields != 3 {
			discardWildcard(c, state)
			return failure("HOST_FAIL", "wildcard kind filtering requires normalized lstat regular, dir and link fields")
		}
		if mask&host.DiscoveryKind(regular, directory, link) != 0 {
			state.Values = append(state.Values, state.Matches.List[i].Clone(state.Alloc))
		}
		state.Index++
	}
	value := core.NewList(c.Run, state.Values)
	discardWildcard(c, state)
	return eval.Result{Value: value}
}

func wildcardUnion(c *eval.Context, v []core.Value) eval.Result {
	if len(v) == 1 && v[0].Kind != core.List {
		return fileRequest(c, host.OpWildcard, v[0])
	}
	// Each union member has its own replay identity, so a later suspension
	// neither repeats earlier host reads nor consumes their completions.
	state := wildcardProgress(c, core.Value{})
	for state.Index < len(v) {
		i := state.Index
		previous := c.CallPath

		index := strconv.Itoa(i)
		member := core.NewString(c.Run, previous+"/wildcard:"+index)
		c.CallPath = member.Text
		result := eval.Result{}
		if v[i].Kind == core.List {
			result.Value = v[i].Clone(c.Run)
		} else {
			result = fileRequest(c, host.OpWildcard, v[i])
		}
		c.CallPath = previous
		member.Free(c.Run)
		if result.Waiting {
			if !state.Tracked {
				discardWildcard(c, state)
			}
			return result
		}
		if result.Diagnostic.Code != "" {
			discardWildcard(c, state)
			return result
		}
		for j := range result.Value.List {
			state.Values = append(state.Values, result.Value.List[j].Clone(state.Alloc))
		}
		result.Value.Free(c.Run)
		state.Index++
	}
	state.Values = sortedWildcardPaths(state.Alloc, state.Values)
	out := core.NewList(c.Run, state.Values)
	discardWildcard(c, state)
	return eval.Result{Value: out}
}

func sortedWildcardPaths(a mem.Allocator, paths []core.Value) []core.Value {
	// Stable order retains the first value's literal-path provenance on ties.
	slices.SortStableFunc(paths, compareWildcardPaths)
	count := 0
	for _, item := range paths {
		if count != 0 && paths[count-1].Text == item.Text {
			item.Free(a)
		} else {
			paths[count] = item
			count++
		}
	}
	return paths[:count]
}

func compareWildcardPaths(a core.Value, b core.Value) int {
	return strings.Compare(a.Text, b.Text)
}
func opWrite(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	path := ""
	if v[0].Kind == core.Resource && v[0].Resource.Kind == core.ResourceFile {
		path = v[0].Resource.Name
	} else if v[0].IsPathText() {
		path = v[0].Text
	} else {
		return invalidArgument(c, v, 0, "string or file resource")
	}
	if !c.Allows(eval.Write, path) {
		return failure("CAP_DENIED", "write access denied")
	}
	// Bytes write raw; every other coercible value renders as with str.
	if v[1].Kind == core.Bytes {
		return writeBytes(c, path, v[1].Bytes)
	}
	text, ok := stringValue(c.Run, v[1], false)
	if !ok {
		return invalidArgument(c, v, 1, "bytes or text-coercible value")
	}
	result := writeBytes(c, path, []byte(text))
	mem.FreeString(c.Run, text)
	return result
}
func writeBytes(c *eval.Context, path string, data []byte) eval.Result {
	for i := range c.RuleFrames {
		if c.RuleFrames[i].ServiceRule {
			return failure("EXPR_INVALID", "services cannot publish file outputs")
		}
	}
	if !c.Finalizing && (c.Phase == eval.RenderingPhase || (c.RecipeNode != 0 && len(c.RuleFrames) != 0)) {
		c.EmitWrite(path, data)
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if c.Phase == eval.PlanningPhase || c.Phase == eval.ResolvingPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "write is invalid while planning")
	}
	if c.Program != nil && c.Program.DryRun {
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	return request(c, host.RequestWriteFile, host.WritePayload(c.Run, path, data))
}
func opEnv(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
	if !c.Allows(eval.Env, v[0].Text) {
		return c.EnvironmentDenied(v[0].Text)
	}
	if value, written := c.EnvironmentEdit(v[0].Text); written {
		return eval.Result{Value: value}
	}
	if c.HasEnvironment {
		// The snapshot supplies the value; only the requested name is observed.
		for i := range c.Environment {
			assignment := c.Environment[i]
			equal := strings.IndexByte(assignment, '=')
			if equal >= 0 && assignment[:equal] == v[0].Text {
				if c.Engine != nil {
					c.Engine.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: v[0].Text}, core.ValueSignature(core.Value{Kind: core.String, Text: assignment[equal+1:]}))
				}
				return eval.Result{Value: core.NewString(c.Run, assignment[equal+1:])}
			}
		}
		if c.Engine != nil {
			c.Engine.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: v[0].Text}, core.ValueSignature(core.Value{Kind: core.Nil}))
		}
		return eval.Result{Value: core.Value{Kind: core.Nil}}
	}
	if !c.DirectHostRequests && !dependency(c, core.ResourceEnvironment, v[0].Text) {
		return eval.Result{Waiting: true}
	}
	return request(c, host.RequestEnvironment, core.NewString(c.Run, v[0].Text))
}
func opSh(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	command := len(v) - 1
	if c.Phase != eval.EvaluatePhase {
		c.MarkPhaseInvalid()
		discardArgCallables(c, v)
		return failure("PHASE_INVALID", "sh! is invalid outside evaluation")
	}
	if v[command].Kind != core.String {
		return invalidArgument(c, v, command, "string")
	}
	if len(v) == 2 && v[0].Kind != core.Record {
		return invalidArgument(c, v, 0, "record")
	}
	if len(v) == 2 && len(v[0].Record) != 0 {
		discardArgCallables(c, v)
		return c.InvalidOperation("options are not supported; omit OPTIONS or use an empty record")
	}
	if c.Program != nil && c.Program.DryRun {
		return eval.Result{Value: core.NewString(c.Run, "")}
	}
	return request(c, host.RequestProcess, c.ShellPayload(v[command].Text))
}

func opShellTemplate(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if c.Phase != eval.EvaluatePhase {
		c.MarkPhaseInvalid()
		discardArgCallables(c, v)
		return failure("PHASE_INVALID", "shell-template is invalid outside evaluation")
	}
	if v[0].Kind != core.List {
		return invalidArgument(c, v, 0, "list of strings")
	}
	if v[1].Kind != core.List {
		return invalidArgument(c, v, 1, "list of strings")
	}
	if len(v[0].List) != len(v[1].List)+1 {
		discardArgCallables(c, v)
		return failure("EXPR_INVALID", "shell-template requires one more fragment than value")
	}
	for i := range v[0].List {
		if v[0].List[i].Kind != core.String {
			discardArgCallables(c, v)
			return failure("EXPR_INVALID", "shell-template fragments must be strings")
		}
	}
	for i := range v[1].List {
		if v[1].List[i].Kind != core.String {
			discardArgCallables(c, v)
			return failure("EXPR_INVALID", "shell-template values must be strings")
		}
	}
	if c.Program != nil && c.Program.DryRun {
		return eval.Result{Value: core.NewString(c.Run, "")}
	}
	b := strings.Builder{}
	defer b.Reset()
	for i := range v[0].List {
		b.WriteString(v[0].List[i].Text)
		if i == len(v[1].List) {
			break
		}
		b.WriteByte('\'')
		value := v[1].List[i].Text
		for j := 0; j < len(value); j++ {
			if value[j] == '\'' {
				b.WriteString("'\\''")
			} else {
				b.WriteByte(value[j])
			}
		}
		b.WriteByte('\'')
	}
	return request(c, host.RequestProcess, c.ShellPayload(b.String()))
}

// opNow reads the host wall clock as nanoseconds since the Unix epoch. Clocks
// are impure, so they are invalid while planning or resolving a graph; the
// request stays a host operation rather than a cached value.
func opNow(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	_ = v
	if c.Phase == eval.PlanningPhase || c.Phase == eval.ResolvingPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "now is invalid while planning")
	}
	return request(c, host.RequestWallTime, core.Value{Kind: core.Nil})
}

// opMonotonic reads the host monotonic clock as nanoseconds from an arbitrary
// origin, for measuring durations without wall-clock jumps.
func opMonotonic(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	_ = v
	if c.Phase == eval.PlanningPhase || c.Phase == eval.ResolvingPhase {
		c.MarkPhaseInvalid()
		return failure("PHASE_INVALID", "monotonic is invalid while planning")
	}
	return request(c, host.RequestMonotonicTime, core.Value{Kind: core.Nil})
}
