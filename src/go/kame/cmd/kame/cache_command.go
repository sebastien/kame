package main

import (
	"bytes"
	"io"
	"kame/cli"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"os"
	"path"

	"strconv"
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
	var buckets = [4]cacheBucket{
		{Name: "tasks", Directory: "tasks"},
		{Name: "host", Directory: "host"},
		{Name: "file-context", Directory: "file-context"},
		{Name: "timing", Directory: "timing"},
	}
	if parsed.Args[0] == "list" {
		var records []cacheListEntry
		for i := range buckets {
			name := path.Join(parsed.Directory, ".kame/cache/"+buckets[i].Directory)
			entries, err := os.ReadDir(name)
			if os.IsNotExist(err) {
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
				file := path.Join(name, entries[j].Name())
				info, statErr := os.Lstat(file)
				if statErr == nil && info.Mode().IsRegular() {
					records = append(records, cacheListEntry{Backend: buckets[i].Name, Key: cloneCommandText(entries[j].Name()), Bytes: info.Size()})
				} else if statErr != nil && !os.IsNotExist(statErr) {
					mem.FreeString(mem.System, file)

					mem.FreeString(mem.System, name)
					freeCacheList(records)
					cliError(errOut, "FS_ERR", "cannot inspect cache record")
					return 1
				}
				mem.FreeString(mem.System, file)
			}

			mem.FreeString(mem.System, name)
		}
		var buffer = bytes.Buffer{}
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
		buffer.Reset()
		freeCacheList(records)
		return 0
	}
	removed := 0
	wouldRemove := 0
	for i := range buckets {
		name := path.Join(parsed.Directory, ".kame/cache/"+buckets[i].Directory)
		entries, err := os.ReadDir(name)
		if os.IsNotExist(err) {
			mem.FreeString(mem.System, name)
			continue
		}
		if err != nil {
			mem.FreeString(mem.System, name)
			cliError(errOut, "FS_ERR", "cannot inspect cache directory")
			return 1
		}
		for j := range entries {
			file := path.Join(name, entries[j].Name())
			info, statErr := os.Lstat(file)
			if statErr == nil && info.Mode().IsRegular() {
				if parsed.DryRun {
					wouldRemove++
					mem.FreeString(mem.System, file)
					continue
				}
				removeErr := os.Remove(file)
				if removeErr == nil {
					removed++
				} else if !os.IsNotExist(removeErr) {
					mem.FreeString(mem.System, file)

					mem.FreeString(mem.System, name)
					cliError(errOut, "FS_ERR", "cannot remove cache record")
					return 1
				}
			} else if statErr != nil && !os.IsNotExist(statErr) {
				mem.FreeString(mem.System, file)

				mem.FreeString(mem.System, name)
				cliError(errOut, "FS_ERR", "cannot inspect cache record")
				return 1
			}
			mem.FreeString(mem.System, file)
		}

		mem.FreeString(mem.System, name)
	}

	if cliDiagnosticJSON {
		e := json.NewEncoder(out)
		e.BeginObject()
		e.Str("schema")
		e.Int(1)
		e.Str("type")
		e.Str("cache-clean-result")
		e.Str("removed")
		e.Int(int64(removed))
		if parsed.DryRun {
			e.Str("wouldRemove")
			e.Int(int64(wouldRemove))
			e.Str("dryRun")
			e.Bool(true)
		}
		e.EndObject()
		e.Flush()
		io.WriteString(out, "\n")
	} else {
		if parsed.DryRun {
			io.WriteString(errOut, "cache clean · would remove "+strconv.Itoa(wouldRemove)+" cache records\n")
			return 0
		}
		cli.Style(errOut, "status.success", "done ", diagnosticColor == "always")
		io.WriteString(errOut, "cache clean · removed "+strconv.Itoa(removed)+" cache records\n")
	}
	return 0
}

func freeCacheList(records []cacheListEntry) {
	for i := range records {
		mem.FreeString(mem.System, records[i].Key)
	}
	mem.FreeSlice(nil, records)
}
