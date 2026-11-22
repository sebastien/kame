package host

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"path"
	"slices"
	"strings"
)

const (
	DiscoveryFile = 1 << iota
	DiscoveryDirectory
	DiscoveryLink
	DiscoveryOther
	DiscoveryAll = DiscoveryFile | DiscoveryDirectory | DiscoveryLink | DiscoveryOther
)

// DeclarationDiscoveryKey qualifies one tracked scan by its kind mask. A plain
// declaration key selects regular files.
func DeclarationDiscoveryKey(a mem.Allocator, key string, kinds int) string {
	if kinds == DiscoveryFile {
		return core.NewString(a, key).Text
	}
	const digits = "0123456789abcdef"
	return core.NewString(a, "\x00"+digits[kinds:kinds+1]+"\x00"+key).Text
}

func DeclarationDiscovery(key string) (string, int) {
	if len(key) > 3 && key[0] == 0 && key[2] == 0 {
		const digits = "0123456789abcdef"
		if kind := strings.IndexByte(digits, key[1]); kind >= 0 {
			return key[3:], kind
		}
	}
	return key, DiscoveryFile
}

func DiscoveryKind(regular, directory, link bool) int {
	if link {
		return DiscoveryLink
	}
	if directory {
		return DiscoveryDirectory
	}
	if regular {
		return DiscoveryFile
	}
	return DiscoveryOther
}

// DiscoveryKey keeps the exact matcher in the resource identity while allowing
// hosts to enumerate candidates using their ordinary wildcard operation.
func DiscoveryKey(a mem.Allocator, scan, pattern, cwd, root string) string {
	return core.NewString(a, scan+"\x00"+pattern+"\x00"+cwd+"\x00"+root).Text
}

// CommandDiscoveryKey prevents command scans from sharing ordinary wildcard
// dependencies, while preserving the same scan spelling on the host wire.
func CommandDiscoveryKey(a mem.Allocator, scan, root string) string {
	return core.NewString(a, scan+"\x00"+root+"\x00").Text
}

func CommandDiscovery(key string) (scan, root string, command bool) {
	scan, rest, found := strings.Cut(key, "\x00")
	if found && scan != "" && strings.HasSuffix(rest, "\x00") && strings.Count(rest, "\x00") == 1 {
		return scan, rest[:len(rest)-1], true
	}
	return key, "", false
}

func splitDiscoveryKey(key string) (scan, pattern, cwd, root string, grouped bool) {
	scan, rest, found := strings.Cut(key, "\x00")
	if !found {
		return key, "", "", "", false
	}
	pattern, rest, found = strings.Cut(rest, "\x00")
	if !found {
		return key, "", "", "", false
	}
	cwd, root, found = strings.Cut(rest, "\x00")
	if !found || scan == "" || pattern == "" || strings.ContainsRune(root, 0) {
		return key, "", "", "", false
	}
	return scan, pattern, cwd, root, true
}

// DiscoveryScan returns the ordinary wildcard to send to the filesystem host.
func DiscoveryScan(key string) string {
	if scan, _, command := CommandDiscovery(key); command {
		return scan
	}
	scan, _, _, _, _ := splitDiscoveryKey(key)
	return scan
}

func DiscoveryPattern(key string) string {
	_, pattern, _, _, grouped := splitDiscoveryKey(key)
	if grouped {
		return pattern
	}
	return DiscoveryScan(key)
}

// ParseDiscoveryPattern returns the normalized matcher for a grouped key.
// Ordinary filesystem globs have no separate matcher.
func ParseDiscoveryPattern(a mem.Allocator, key string) expr.PatternParse {
	_, pattern, cwd, _, grouped := splitDiscoveryKey(key)
	if !grouped {
		return expr.PatternParse{}
	}
	parsed := expr.ParsePathPatternText(a, pattern)
	if !core.IsResourceURIName(pattern) {
		expr.NormalizePathPatternRoot(a, parsed.Pattern, cwd)
	}
	return parsed
}

// FilterDiscovery takes ownership of a host's candidate list. Only exact
// matches enter the published resource value and its membership signature.
func FilterDiscovery(a mem.Allocator, key string, candidates core.Value) (core.Value, diagnostic.Diagnostic) {
	if scan, root, command := CommandDiscovery(key); command {
		return filterCommandDiscovery(a, scan, root, candidates)
	}
	_, _, cwd, root, grouped := splitDiscoveryKey(key)
	if !grouped {
		return candidates, diagnostic.Diagnostic{}
	}
	defer candidates.Free(a)
	if candidates.Kind != core.List {
		return core.Value{}, diagnostic.Diagnostic{Code: "HOST_FAIL", Severity: diagnostic.Error, Message: "invalid wildcard observation"}
	}
	parsed := ParseDiscoveryPattern(a, key)
	defer parsed.Pattern.Free(a)
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern.References != 0 {
		return core.Value{}, diagnostic.Diagnostic{Code: "PAT_INVALID", Severity: diagnostic.Error, Message: "invalid discovery pattern"}
	}
	compiled := expr.CompilePattern(a, parsed.Pattern)
	defer compiled.Free(a)
	base := path.Clean(root)
	defer mem.FreeString(a, base)
	underBase := strings.TrimSuffix(base, "/") + "/"
	var matches []core.Value
	defer func() { mem.FreeSlice(nil, matches) }()
	for _, candidate := range candidates.List {
		if candidate.Kind != core.String {
			return core.Value{}, diagnostic.Diagnostic{Code: "HOST_FAIL", Severity: diagnostic.Error, Message: "invalid wildcard observation"}
		}
		subject := candidate.Text
		uri := core.IsResourceURIName(subject)
		if !uri && !path.IsAbs(subject) {
			subject = path.Join(root, subject)
		}
		result := compiled.MatchText(a, subject)
		mem.FreeSlice(nil, result.Captures)
		if result.Limited {
			return core.Value{}, diagnostic.Diagnostic{Code: "PAT_LIMIT", Severity: diagnostic.Error, Message: "discovery match exceeded its step budget"}
		}
		if result.Matched {
			if !uri {
				candidate.Text = subject
				if cwd == root {
					if suffix, inside := strings.CutPrefix(subject, underBase); inside {
						candidate.Text = "./" + suffix
					} else if base == "." && !path.IsAbs(subject) {
						candidate.Text = "./" + subject
					}
				}
			}
			matches = append(matches, candidate)
		}
	}
	slices.SortFunc(matches, func(a, b core.Value) int { return strings.Compare(a.Text, b.Text) })
	matches = slices.CompactFunc(matches, func(a, b core.Value) bool { return a.Text == b.Text })
	return core.NewList(a, matches), diagnostic.Diagnostic{}
}

func GlobExplicitDot(segment string) bool {
	return strings.HasPrefix(segment, ".") || strings.HasPrefix(segment, `\.`)
}

// Older forwarded hosts enumerate ordinary wildcard candidates. Apply command
// visibility before publishing the observation or running any kind checks.
func filterCommandDiscovery(a mem.Allocator, scan, root string, candidates core.Value) (core.Value, diagnostic.Diagnostic) {
	defer candidates.Free(a)
	if candidates.Kind != core.List {
		return core.Value{}, diagnostic.Diagnostic{Code: "HOST_FAIL", Severity: diagnostic.Error, Message: "invalid wildcard observation"}
	}
	pattern := path.Clean(scan)
	defer mem.FreeString(a, pattern)
	var matches []core.Value
	defer func() { mem.FreeSlice(nil, matches) }()
	for _, candidate := range candidates.List {
		if candidate.Kind != core.String {
			return core.Value{}, diagnostic.Diagnostic{Code: "HOST_FAIL", Severity: diagnostic.Error, Message: "invalid wildcard observation"}
		}
		subject := path.Clean(candidate.Text)
		if path.IsAbs(pattern) && !path.IsAbs(subject) {
			mem.FreeString(a, subject)
			subject = path.Join(root, candidate.Text)
		}
		if !(strings.HasPrefix(subject, ".") || strings.Contains(subject, "/.")) || MatchCommandGlob(pattern, subject) {
			matches = append(matches, candidate)
		}
		mem.FreeString(a, subject)
	}
	return core.NewList(a, matches), diagnostic.Diagnostic{}
}

// MatchCommandGlob matches normalized paths without consuming implicit dots.
func MatchCommandGlob(pattern, match string) bool {
	parts, names := strings.Split(pattern, "/"), strings.Split(match, "/")
	defer mem.FreeSlice(nil, parts)
	defer mem.FreeSlice(nil, names)
	// Each reachable index is a prefix match after the current pattern part.
	reached := make([]bool, len(names)+1)
	defer mem.FreeSlice(nil, reached)
	reached[0] = true
	for _, part := range parts {
		if part == "**" {
			for i, name := range names {
				reached[i+1] = reached[i+1] || reached[i] && !strings.HasPrefix(name, ".")
			}
			continue
		}
		dot := GlobExplicitDot(part)
		for i := len(names) - 1; i >= 0; i-- {
			matched, _ := path.Match(part, names[i])
			reached[i+1] = reached[i] && matched && (dot || !strings.HasPrefix(names[i], "."))
		}
		reached[0] = false
	}
	return reached[len(names)]
}
