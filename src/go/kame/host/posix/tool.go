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
	var startup []string
	if environment == nil {
		startup = Environment(a)
		environment = startup
	}
	result := ResolveToolForPlatform(a, name, cwd, environment, km_cli_environment_names_case_insensitive())
	FreeEnvironment(a, startup)
	return result
}

func toolEnvironmentValue(environment []string, name string, windows bool) string {
	for i := range environment {
		at := strings.IndexByte(environment[i], '=')
		if at > 0 && EnvironmentNameEqualForPlatform(environment[i][:at], name, windows) {
			return environment[i][at+1:]
		}
	}
	return ""
}

// ToolPath applies native slash, drive-root and UNC policy to an owned path.
// Drive-relative paths and rooted Windows paths without a cwd drive are rejected.
// Forward /[A-Za-z] followed by '/' or end is reserved for Cosmo drive aliases;
// native backslash-rooted names (including \C\...) retain rooted-path meaning.
func ToolPath(a mem.Allocator, cwd string, name string, windows bool) string {
	if !windows {
		if path.IsAbs(name) {
			return path.Clean(a, name)
		}
		return path.Join(a, cwd, name)
	}
	base := toolWindowsPath(a, cwd)
	defer mem.FreeString(a, base)
	value := toolWindowsPath(a, name)
	defer mem.FreeString(a, value)
	drive := len(value) >= 2 && value[1] == ':'
	if drive && (len(value) < 3 || value[2] != '/') {
		return ""
	}
	absolute := path.IsAbs(value) || drive
	unc := strings.HasPrefix(value, "//") || (!absolute && strings.HasPrefix(base, "//"))
	result := ""
	if absolute && !drive && !unc {
		if len(base) < 3 || base[1] != ':' || base[2] != '/' {
			return ""
		}
		result = path.Clean(a, base[:2]+value)
	} else if absolute {
		result = path.Clean(a, value)
	} else {
		result = path.Join(a, base, value)
	}
	// POSIX path.Clean removes a drive root's slash; C: is drive-relative.
	if len(result) == 2 && result[1] == ':' {
		root := cloneText(a, result+"/")
		mem.FreeString(a, result)
		return root
	}
	if unc {
		preserved := cloneText(a, "/"+result)
		mem.FreeString(a, result)
		return preserved
	}
	return result
}

func toolWindowsPath(a mem.Allocator, name string) string {
	alias := len(name) >= 2 && name[0] == '/' &&
		((name[1] >= 'A' && name[1] <= 'Z') || (name[1] >= 'a' && name[1] <= 'z')) &&
		(len(name) == 2 || name[2] == '/')
	if alias {
		value := name[1:2] + ":" + name[2:]
		if len(name) == 2 {
			value += "/"
		}
		return strings.ReplaceAll(a, value, "\\", "/")
	}
	return strings.ReplaceAll(a, name, "\\", "/")
}

// ResolveToolForPlatform shares native resolution with platform-policy tests.
// Its environment is explicit; only ResolveTool snapshots the native environment.
func ResolveToolForPlatform(a mem.Allocator, name string, cwd string, environment []string, windows bool) string {
	if name == "" {
		return ""
	}
	extensions := toolEnvironmentValue(environment, "PATHEXT", windows)
	if extensions == "" {
		extensions = ".COM;.EXE;.BAT;.CMD"
	}
	if strings.IndexByte(name, '/') >= 0 || (windows && (strings.IndexByte(name, '\\') >= 0 || strings.IndexByte(name, ':') >= 0)) {
		candidate := ToolPath(a, cwd, name, windows)
		return executableCandidate(a, candidate, extensions, windows, true)
	}
	pathValue := toolEnvironmentValue(environment, "PATH", windows)
	separator := byte(':')
	if windows {
		separator = ';'
	}
	for start := 0; start <= len(pathValue); {
		end := start
		for end < len(pathValue) && pathValue[end] != separator {
			end++
		}
		directory := pathValue[start:end]
		base := ToolPath(a, cwd, directory, windows)
		candidate := ""
		if base != "" {
			candidate = ToolPath(a, base, name, windows)
		}
		mem.FreeString(a, base)
		result := executableCandidate(a, candidate, extensions, windows, false)
		if result != "" {
			return result
		}
		if end == len(pathValue) {
			break
		}
		start = end + 1
	}
	return ""
}

// Explicit paths keep exact-first compatibility. Bare PATH names prefer each
// directory's PATHEXT suffixes, then its exact extensionless file.
// executableCandidate consumes candidate, returning the owned match if any.
func executableCandidate(a mem.Allocator, candidate string, extensions string, windows bool, explicit bool) string {
	if candidate == "" {
		return ""
	}
	exactFirst := !windows || explicit || strings.IndexByte(path.Base(candidate), '.') >= 0
	if exactFirst && executableFile(candidate, windows) {
		return candidate
	}
	if windows {
		for start := 0; start <= len(extensions); {
			end := start
			for end < len(extensions) && extensions[end] != ';' {
				end++
			}
			extension := extensions[start:end]
			if len(extension) > 1 && extension[0] == '.' {
				option := cloneText(a, candidate+extension)
				if executableFile(option, true) {
					mem.FreeString(a, candidate)
					return option
				}
				mem.FreeString(a, option)
			}
			if end == len(extensions) {
				break
			}
			start = end + 1
		}
	}
	if windows && !exactFirst && executableFile(candidate, true) {
		return candidate
	}
	mem.FreeString(a, candidate)
	return ""
}

func executableFile(name string, windows bool) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular() && (windows || info.Mode()&0111 != 0)
}
