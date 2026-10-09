package posix

import (
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/strings"
)

// ResolveTool returns an owned executable path. A nil environment selects the
// native startup environment; drivers can pass their captured startup snapshot.
func ResolveTool(a mem.Allocator, name string, cwd string, environment []string) string {
	if name == "" { return "" }
	if strings.IndexByte(name, '/') >= 0 {
		candidate := ""
		if path.IsAbs(name) { candidate = path.Clean(a, name) } else { candidate = path.Join(a, cwd, name) }
		if executableFile(candidate) { return candidate }
		mem.FreeString(a, candidate)
		return ""
	}
	var startup []string
	if environment == nil { startup = Environment(a); environment = startup }
	pathValue := ""
	for i := range environment { if strings.HasPrefix(environment[i], "PATH=") { pathValue = environment[i][5:]; break } }
	for start := 0; start <= len(pathValue); {
		end := start
		for end < len(pathValue) && pathValue[end] != ':' { end++ }
		directory := pathValue[start:end]
		candidate := ""
		if path.IsAbs(directory) { candidate = path.Join(a, directory, name) } else { candidate = path.Join(a, cwd, directory, name) }
		if executableFile(candidate) { FreeEnvironment(a, startup); return candidate }
		mem.FreeString(a, candidate)
		if end == len(pathValue) { break }
		start = end+1
	}
	FreeEnvironment(a, startup)
	return ""
}

func executableFile(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0
}
