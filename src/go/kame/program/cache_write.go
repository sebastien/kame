package program

import (
	"solod.dev/so/slices"
)

func (p *Program) cacheAppend(dst *[]byte, truncated *bool, data []byte, limit int) {
	if limit <= 0 {
		limit = cacheLogDefault
	}
	for i := range data {
		if len(*dst) >= limit {
			*truncated = true
			return
		}
		*dst = slices.Append(p.Alloc, *dst, data[i])
	}
}

func (p *Program) cacheCommit(entry *instance, stdout []byte, stderr []byte, stdoutTruncated bool, stderrTruncated bool) {
	if p.cacheBlockedByBareTask(entry) {
		return
	}
	p.cacheAppend(&entry.CacheStdout, &entry.CacheStdoutTruncated, stdout, p.Options.CacheRetainBytes)
	p.cacheAppend(&entry.CacheStderr, &entry.CacheStderrTruncated, stderr, p.Options.CacheRetainBytes)
	if stdoutTruncated {
		entry.CacheStdoutTruncated = true
	}
	if stderrTruncated {
		entry.CacheStderrTruncated = true
	}
	identity := p.cacheIdentity(entry)
	completed := p.Host.Now()
	started := entry.cacheStartedAt
	if started <= 0 || started > completed {
		started = completed
	}
	record := cacheRecord{Identity: identity, Schema: 1, StartedAt: started, CompletedAt: completed, Duration: completed - started, ExitStatus: 0, Manifest: slices.Clone(p.Alloc, entry.CacheManifest), Stdout: slices.Clone(p.Alloc, entry.CacheStdout), Stderr: slices.Clone(p.Alloc, entry.CacheStderr), StdoutTruncated: entry.CacheStdoutTruncated, StderrTruncated: entry.CacheStderrTruncated}
	for i := range record.Fingerprint {
		record.Fingerprint[i] = entry.CacheFingerprint[i]
	}
	p.cacheSave(entry, &record)
	record.Free(p.Alloc)
	p.releaseCacheLock(entry)
}
