package main

import (
	"kame/cli"
	"solod.dev/so/bytes"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

type cacheListEntry struct {
	Backend string
	Key     string
	Bytes   int64
}

type cacheBucket struct {
	Name      string
	Directory string
}

func runCache(args []string, out io.Writer, errOut io.Writer) int {
	parsed := cli.Parse("cache", args)
	defer parsed.Free()
	if !parsed.OK {
		cliError(errOut, parsed.Error.Code, parsed.Error.Message)
		return 2
	}
	if len(parsed.Args) != 1 {
		cliError(errOut, "OPT_VALUE_INVALID", "cache requires the list or clean action")
		return 2
	}
	var buckets = [3]cacheBucket{
		{Name: "tasks", Directory: "tasks"},
		{Name: "host", Directory: "host"},
		{Name: "file-context", Directory: "file-context"},
	}
	if parsed.Args[0] == "list" {
		var records []cacheListEntry
		for i := range buckets {
			name := path.Join(mem.System, parsed.Directory, ".kame/cache/"+buckets[i].Directory)
			entries, err := os.ReadDir(mem.System, name)
			if err == os.ErrNotExist {
				mem.FreeString(mem.System, name)
				continue
			}
			if err != nil {
				mem.FreeString(mem.System, name)
				freeCacheList(records)
				cliError(errOut, "FS_ERR", "cannot inspect cache directory")
				return 1
			}
			for j := range entries {
				file := path.Join(mem.System, name, entries[j].Name)
				info, statErr := os.Lstat(file)
				if statErr == nil && info.Mode().IsRegular() {
					records = slices.Append(mem.System, records, cacheListEntry{Backend: buckets[i].Name, Key: cloneCommandText(entries[j].Name), Bytes: info.Size()})
				} else if statErr != nil && statErr != os.ErrNotExist {
					mem.FreeString(mem.System, file)
					os.FreeDirEntry(mem.System, entries)
					mem.FreeString(mem.System, name)
					freeCacheList(records)
					cliError(errOut, "FS_ERR", "cannot inspect cache record")
					return 1
				}
				mem.FreeString(mem.System, file)
			}
			os.FreeDirEntry(mem.System, entries)
			mem.FreeString(mem.System, name)
		}
		var buffer = bytes.NewBuffer(mem.System, nil)
		e := json.NewEncoder(&buffer)
		e.BeginArray()
		for i := range records {
			e.BeginObject()
			e.Str("backend")
			e.Str(records[i].Backend)
			e.Str("key")
			e.Str(records[i].Key)
			e.Str("bytes")
			e.Int(records[i].Bytes)
			e.EndObject()
		}
		e.EndArray()
		e.Flush()
		if cliDiagnosticJSON {
			io.WriteString(out, buffer.String())
			io.WriteString(out, "\n")
		} else {
			cli.WriteReport(out, "cache list", buffer.String(), stdoutColor)
		}
		buffer.Free()
		freeCacheList(records)
		return 0
	}
	removed := 0
	for i := range buckets {
		name := path.Join(mem.System, parsed.Directory, ".kame/cache/"+buckets[i].Directory)
		entries, err := os.ReadDir(mem.System, name)
		if err == os.ErrNotExist {
			mem.FreeString(mem.System, name)
			continue
		}
		if err != nil {
			mem.FreeString(mem.System, name)
			cliError(errOut, "FS_ERR", "cannot inspect cache directory")
			return 1
		}
		for j := range entries {
			file := path.Join(mem.System, name, entries[j].Name)
			info, statErr := os.Lstat(file)
			if statErr == nil && info.Mode().IsRegular() {
				removeErr := os.Remove(file)
				if removeErr == nil {
					removed++
				} else if removeErr != os.ErrNotExist {
					mem.FreeString(mem.System, file)
					os.FreeDirEntry(mem.System, entries)
					mem.FreeString(mem.System, name)
					cliError(errOut, "FS_ERR", "cannot remove cache record")
					return 1
				}
			} else if statErr != nil && statErr != os.ErrNotExist {
				mem.FreeString(mem.System, file)
				os.FreeDirEntry(mem.System, entries)
				mem.FreeString(mem.System, name)
				cliError(errOut, "FS_ERR", "cannot inspect cache record")
				return 1
			}
			mem.FreeString(mem.System, file)
		}
		os.FreeDirEntry(mem.System, entries)
		mem.FreeString(mem.System, name)
	}
	buffer := make([]byte, 24)
	if cliDiagnosticJSON {
		e := json.NewEncoder(out)
		e.BeginObject()
		e.Str("schema")
		e.Int(1)
		e.Str("type")
		e.Str("cache-clean-result")
		e.Str("removed")
		e.Int(int64(removed))
		e.EndObject()
		e.Flush()
		io.WriteString(out, "\n")
	} else {
		cli.Style(errOut, "status.success", "done ", diagnosticColor == "always")
		io.WriteString(errOut, "cache clean · removed "+strconv.Itoa(buffer, removed)+" cache records\n")
	}
	return 0
}

func freeCacheList(records []cacheListEntry) {
	for i := range records {
		mem.FreeString(mem.System, records[i].Key)
	}
	slices.Free(mem.System, records)
}
