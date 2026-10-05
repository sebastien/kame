package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

// ResourceURI is a canonical protocol identity. Its strings belong to the
// allocator that parsed it and are released by Free.
type ResourceURI struct {
	Allocator mem.Allocator
	Scheme    string
	Authority string
	Path      string
}

func (u *ResourceURI) Free() {
	if u == nil {
		return
	}
	mem.FreeString(u.Allocator, u.Scheme)
	mem.FreeString(u.Allocator, u.Authority)
	mem.FreeString(u.Allocator, u.Path)
	*u = ResourceURI{}
}

// Canonical returns the normalized URI string using the supplied allocator.
func (u *ResourceURI) Canonical(a mem.Allocator) string {
	if u == nil || u.Scheme == "" {
		return ""
	}
	out := strings.NewBuilder(a)
	out.WriteString(u.Scheme)
	out.WriteString("://")
	out.WriteString(u.Authority)
	out.WriteString(u.Path)
	text := cloneResourceURIText(a, out.String())
	out.Free()
	return text
}

// ResourceURIResult carries either a canonical URI or the byte offset of an
// invalid component. Error is stable and suitable for a source diagnostic.
type ResourceURIResult struct {
	URI    ResourceURI
	Error  string
	Offset int
}

// IsResourceURIName reports whether a stored resource identity uses a supported
// URI scheme prefix. Resource values are validated before they are created.
func IsResourceURIName(name string) bool {
	return hasResourceURIPrefix(name, "file://") || hasResourceURIPrefix(name, "mem://")
}

func hasResourceURIPrefix(text string, prefix string) bool {
	if len(text) < len(prefix) {
		return false
	}
	return text[:len(prefix)] == prefix
}

// ParseResourceURI accepts the file and mem protocols and normalizes URI path
// segments before identity is used by the runtime.
func ParseResourceURI(a mem.Allocator, text string) ResourceURIResult {
	separator := -1
	for i := 0; i+2 < len(text); i++ {
		if text[i] == ':' && text[i+1] == '/' && text[i+2] == '/' {
			separator = i
			break
		}
	}
	if separator <= 0 {
		return resourceURIError("URI must use scheme://authority/path", 0)
	}
	scheme := text[:separator]
	for i := range scheme {
		b := scheme[i]
		if b < 'a' || b > 'z' {
			return resourceURIError("URI scheme must be lowercase ASCII", i)
		}
	}
	if scheme != "file" && scheme != "mem" {
		return resourceURIError("unsupported resource URI scheme", 0)
	}
	authorityStart := separator + 3
	pathStart := authorityStart
	for pathStart < len(text) && text[pathStart] != '/' {
		if text[pathStart] == '?' || text[pathStart] == '#' {
			return resourceURIError("query and fragment components are not supported", pathStart)
		}
		pathStart++
	}
	authority := text[authorityStart:pathStart]
	if scheme == "file" && authority != "" {
		return resourceURIError("file URI authority must be empty", authorityStart)
	}
	if scheme == "mem" {
		if authority == "" {
			return resourceURIError("mem URI requires a namespace", authorityStart)
		}
		for i := range authority {
			b := authority[i]
			if !uriAuthorityByte(b) {
				return resourceURIError("invalid mem URI namespace", authorityStart+i)
			}
		}
	}
	if pathStart == len(text) {
		return resourceURIError("resource URI requires an absolute path", pathStart)
	}
	for i := pathStart; i < len(text); i++ {
		if text[i] == '?' || text[i] == '#' {
			return resourceURIError("query and fragment components are not supported", i)
		}
	}
	decoded := decodeURIPath(a, text[pathStart:])
	if !decoded.OK {
		return resourceURIError("invalid percent escape or UTF-8 path", pathStart+decoded.ErrorOffset)
	}
	normalized := normalizeURIPath(a, decoded.Text)
	mem.FreeString(a, decoded.Text)
	if !normalized.OK {
		return resourceURIError("resource URI path escapes its root", pathStart+normalized.ErrorOffset)
	}
	result := ResourceURI{Allocator: a}
	result.Scheme = cloneResourceURIText(a, scheme)
	result.Authority = cloneResourceURIText(a, authority)
	result.Path = normalized.Text
	return ResourceURIResult{URI: result}
}

func resourceURIError(message string, offset int) ResourceURIResult {
	return ResourceURIResult{Error: message, Offset: offset}
}

func uriAuthorityByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.'
}

type uriPathResult struct {
	Text        string
	ErrorOffset int
	OK          bool
}

func decodeURIPath(a mem.Allocator, input string) uriPathResult {
	buffer := mem.AllocSlice[byte](a, len(input), len(input))
	count := 0
	for i := 0; i < len(input); i++ {
		if input[i] != '%' {
			buffer[count] = input[i]
			count++
			continue
		}
		if i+2 >= len(input) {
			mem.FreeSlice(a, buffer)
			return uriPathResult{ErrorOffset: i}
		}
		hi := uriHex(input[i+1])
		lo := uriHex(input[i+2])
		if !hi.OK || !lo.OK {
			mem.FreeSlice(a, buffer)
			return uriPathResult{ErrorOffset: i}
		}
		buffer[count] = hi.Value<<4 | lo.Value
		count++
		i += 2
	}
	if !utf8.Valid(buffer[:count]) {
		mem.FreeSlice(a, buffer)
		return uriPathResult{ErrorOffset: 0}
	}
	decoded := string(buffer[:count])
	// The returned string owns a copy; release the temporary decode buffer.
	decoded = cloneResourceURIText(a, decoded)
	mem.FreeSlice(a, buffer)
	return uriPathResult{Text: decoded, OK: true}
}

type uriHexResult struct {
	Value byte
	OK    bool
}

func uriHex(b byte) uriHexResult {
	if b >= '0' && b <= '9' {
		return uriHexResult{Value: b - '0', OK: true}
	}
	if b >= 'a' && b <= 'f' {
		return uriHexResult{Value: b - 'a' + 10, OK: true}
	}
	if b >= 'A' && b <= 'F' {
		return uriHexResult{Value: b - 'A' + 10, OK: true}
	}
	return uriHexResult{}
}

func normalizeURIPath(a mem.Allocator, input string) uriPathResult {
	if len(input) == 0 || input[0] != '/' {
		return uriPathResult{ErrorOffset: 0}
	}
	var segments []string
	start := 1
	for i := 1; i <= len(input); i++ {
		if i != len(input) && input[i] != '/' {
			continue
		}
		segment := input[start:i]
		if segment == ".." {
			if len(segments) == 0 {
				slices.Free(a, segments)
				return uriPathResult{ErrorOffset: start}
			}
			mem.FreeString(a, segments[len(segments)-1])
			segments = segments[:len(segments)-1]
		} else if segment != "" && segment != "." {
			segments = slices.Append(a, segments, cloneResourceURIText(a, segment))
		}
		start = i + 1
	}
	out := strings.NewBuilder(a)
	out.WriteString("/")
	for i := range segments {
		if i != 0 {
			out.WriteString("/")
		}
		out.WriteString(segments[i])
	}
	canonical := cloneResourceURIText(a, out.String())
	out.Free()
	for i := range segments {
		mem.FreeString(a, segments[i])
	}
	slices.Free(a, segments)
	return uriPathResult{Text: canonical, OK: true}
}

func cloneResourceURIText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	bytes := mem.AllocSlice[byte](a, len(text), len(text))
	copy(bytes, text)
	return string(bytes)
}
