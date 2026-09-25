// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

type buildSource struct {
	Name      string
	Text      string
	Data      []byte
	OwnedName bool
	Missing   bool
	Status    int
}

func freeBuildSource(source *buildSource) {
	if source == nil {
		return
	}
	if len(source.Data) != 0 {
		mem.FreeSlice(mem.System, source.Data)
	}
	if source.OwnedName && source.Name != "" {
		mem.FreeString(mem.System, source.Name)
	}
	*source = buildSource{}
}

func loadBuildSource(options buildArguments, errOut io.Writer, reportMissing bool) buildSource {
	if options.Command != "" {
		return buildSource{Name: "<command>", Text: options.Command}
	}
	if options.File != "" {
		return readBuildSource(options.File, errOut)
	}
	candidates := []string{"Makefile.lmk", "make.lmk", "src/lmk/main.lmk"}
	for i := range candidates {
		candidate := path.Join(mem.System, options.Directory, candidates[i])
		_, statErr := os.Stat(candidate)
		if statErr == nil {
			result := readBuildSource(candidate, errOut)
			result.Name = cloneCommandText(candidate)
			result.OwnedName = true
			mem.FreeString(mem.System, candidate)
			return result
		}
		mem.FreeString(mem.System, candidate)
	}
	if reportMissing {
		cliError(errOut, "BUILD_NO_SOURCE", "no build source found (tried Makefile.lmk, make.lmk, src/lmk/main.lmk)")
	}
	return buildSource{Missing: true, Status: 1}
}

func cloneCommandText(text string) string {
	if text == "" {
		return ""
	}
	data := mem.AllocSlice[byte](mem.System, len(text), len(text))
	copy(data, []byte(text))
	return string(data)
}

// mergeEnvironment gives explicit CLI entries precedence without relying on
// duplicate-variable behavior in execve implementations.
func mergeEnvironment(current []string, overrides []string) []string {
	for i := range overrides {
		entry := overrides[i]
		nameEnd := 0
		for nameEnd < len(entry) && entry[nameEnd] != '=' {
			nameEnd++
		}
		replaced := false
		for j := range current {
			if len(current[j]) <= nameEnd || current[j][nameEnd] != '=' {
				continue
			}
			if current[j][:nameEnd] != entry[:nameEnd] {
				continue
			}
			mem.FreeString(mem.System, current[j])
			current[j], replaced = cloneCommandText(entry), true
			break
		}
		if !replaced {
			current = slices.Append(mem.System, current, cloneCommandText(entry))
		}
	}
	return current
}

func readBuildSource(name string, errOut io.Writer) buildSource {
	data, readErr := os.ReadFile(mem.System, name)
	if readErr != nil {
		cliError(errOut, "FS_ERR", "cannot read source: "+name)
		return buildSource{Status: 1}
	}
	return buildSource{Name: name, Text: string(data), Data: data}
}
