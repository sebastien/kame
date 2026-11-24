package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const historyHeader = "kash-history-v1\n"

type historyScope struct {
	Root string `json:"root"`
	Kind string `json:"kind"`
}

type historyEntry struct {
	ID    string       `json:"id"`
	Time  time.Time    `json:"time"`
	Text  string       `json:"text"`
	CWD   string       `json:"cwd"`
	Scope historyScope `json:"scope"`
}

type replHistory struct {
	path     string
	maxAge   time.Duration
	maxBytes int
	global   bool
	disabled bool
	warned   bool
	entries  []historyEntry // screened store snapshot or memory-only entries
	view     []historyEntry // stable indices during a readline invocation
	expireAt time.Time
	cwd      string
	scope    historyScope
	now      func() time.Time
	out      io.Writer
	options  *replOptions
}

func (h *replHistory) applyOptions() {
	if h.options == nil {
		return
	}
	for _, option := range replOptionRegistry {
		value, _ := h.options.value(option)
		switch option.name {
		case "history-scope":
			h.global = value == "global"
		case "history-max-age-days":
			days, _ := strconv.ParseInt(value, 10, 64)
			h.maxAge = time.Duration(days) * 24 * time.Hour
		case "history-max-bytes":
			h.maxBytes, _ = strconv.Atoi(value)
		}
	}
	h.entries = h.prune(h.entries)
	h.selectView()
}

func (h *replHistory) setScope(global bool) {
	h.global = global
	if h.options != nil {
		value := "local"
		if global {
			value = "global"
		}
		h.options.local["history-scope"] = value
	}
}

func loadReplHistory() *replHistory {
	h := &replHistory{maxAge: 15 * 24 * time.Hour, maxBytes: 1 << 20, now: time.Now, out: os.Stderr}
	if path, set := os.LookupEnv("KASH_HISTORY"); set {
		h.path, h.disabled = path, path == ""
	} else {
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				h.disabled = true
			}
			state = filepath.Join(home, ".local", "state")
		}
		h.path = filepath.Join(state, "kash", "history")
	}
	for _, setting := range []string{"KASH_HISTORY_MAX_AGE_DAYS", "KASH_HISTORY_MAX_BYTES"} {
		if value, set := os.LookupEnv(setting); set {
			n, err := strconv.ParseInt(value, 10, 64)
			limit := int64(int(^uint(0)>>1) / 2)
			if setting == "KASH_HISTORY_MAX_AGE_DAYS" {
				limit = int64((1<<63 - 1) / (24 * time.Hour))
			}
			if err != nil || n <= 0 || n > limit {
				h.disabled = true
				h.warning("invalid configuration; history disabled")
				continue
			}
			if setting == "KASH_HISTORY_MAX_BYTES" {
				h.maxBytes = int(n)
			} else {
				h.maxAge = time.Duration(n) * 24 * time.Hour
			}
		}
	}
	if value, set := os.LookupEnv("KASH_HISTORY_SCOPE"); set {
		switch value {
		case "global":
			h.global = true
		case "local":
		default:
			h.disabled = true
			h.warning("invalid configuration; history disabled")
		}
	}
	return h
}

func (h *replHistory) warning(message string) {
	if !h.warned && h.out != nil {
		fmt.Fprintln(h.out, "kash: history: "+message)
		h.warned = true
	}
}

func historyContext(directory string) (string, historyScope) {
	cwd, err := filepath.Abs(directory)
	if err != nil {
		return "", historyScope{}
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", historyScope{}
	}
	for root := cwd; ; root = filepath.Dir(root) {
		git, gitErr := os.Lstat(filepath.Join(root, ".git"))
		jj, jjErr := os.Lstat(filepath.Join(root, ".jj"))
		if gitErr == nil && (git.IsDir() || git.Mode().IsRegular()) || jjErr == nil && jj.IsDir() {
			return cwd, historyScope{Root: root, Kind: "repository"}
		}
		if filepath.Dir(root) == root {
			break
		}
	}
	return cwd, historyScope{Root: cwd, Kind: "directory"}
}

func (h *replHistory) refresh(directory string) {
	h.cwd, h.scope = historyContext(directory)
	if h.disabled {
		h.entries, h.view = nil, nil
		return
	}
	if err := h.update(nil, ""); err != nil {
		h.warning("persistence unavailable; using filtered session memory")
		h.entries = h.prune(h.entries)
	}
	h.selectView()
}

func (h *replHistory) selectView() {
	h.view = nil
	h.expireAt = time.Time{}
	if h.disabled {
		return
	}
	for _, entry := range h.entries {
		if h.global || h.scope.Root != "" && entry.Scope == h.scope {
			h.view = append(h.view, entry)
		}
	}
	if len(h.view) > replHistoryLength {
		h.view = h.view[len(h.view)-replHistoryLength:]
	}
	for _, entry := range h.view {
		deadline := entry.Time.Add(h.maxAge)
		if h.expireAt.IsZero() || deadline.Before(h.expireAt) {
			h.expireAt = deadline
		}
	}
}

func historyEligible(text string) bool {
	if strings.TrimSpace(text) == "" || text[0] == ' ' || text[0] == '\t' || !utf8.ValidString(text) {
		return false
	}
	fields := strings.Fields(text)
	return fields[0] != "history" && fields[0] != "option" && !historySecret(text)
}

func (h *replHistory) valid(entry historyEntry, now time.Time) bool {
	id, err := hex.DecodeString(entry.ID)
	return err == nil && len(id) == 16 && !entry.Time.IsZero() &&
		h.current(entry, now) && historyEligible(entry.Text) &&
		filepath.IsAbs(entry.CWD) && filepath.Clean(entry.CWD) == entry.CWD &&
		filepath.IsAbs(entry.Scope.Root) && filepath.Clean(entry.Scope.Root) == entry.Scope.Root &&
		(entry.Scope.Kind == "directory" && entry.Scope.Root == entry.CWD ||
			entry.Scope.Kind == "repository" && historyWithin(entry.CWD, entry.Scope.Root))
}

func (h *replHistory) current(entry historyEntry, now time.Time) bool {
	return !entry.Time.After(now) && now.Sub(entry.Time) < h.maxAge
}

func historyWithin(cwd, root string) bool {
	relative, err := filepath.Rel(root, cwd)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func historyRecord(entry historyEntry) []byte {
	data, _ := json.Marshal(entry)
	return append(data, '\n')
}

func (h *replHistory) prune(entries []historyEntry) []historyEntry {
	return h.pruneWith(entries, h.valid)
}

func (h *replHistory) pruneWith(entries []historyEntry, accept func(historyEntry, time.Time) bool) []historyEntry {
	now := h.now()
	slices.SortFunc(entries, func(a, b historyEntry) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	sizes := make(map[historyScope]int)
	seen := make(map[string]bool)
	kept := make([]historyEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if seen[entry.ID] || !accept(entry, now) {
			continue
		}
		seen[entry.ID] = true
		size := len(historyRecord(entry))
		if size > h.maxBytes {
			continue
		}
		if sizes[entry.Scope] > h.maxBytes-size {
			// Keep only the newest contiguous suffix per scope.
			sizes[entry.Scope] = h.maxBytes
			continue
		}
		sizes[entry.Scope] += size
		kept = append(kept, entry)
	}
	slices.Reverse(kept)
	return kept
}

func (h *replHistory) record(text string) {
	text = strings.TrimRight(text, " \t\r\n")
	if h.disabled || h.cwd == "" || !historyEligible(text) {
		return
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		h.warning("cannot generate entry identity; entry not recorded")
		return
	}
	entry := historyEntry{ID: hex.EncodeToString(id[:]), Time: h.now().UTC(), Text: text, CWD: h.cwd, Scope: h.scope}
	if len(historyRecord(entry)) > h.maxBytes {
		return
	}
	if err := h.update(&entry, ""); err != nil {
		h.warning("persistence unavailable; using filtered session memory")
		h.entries = h.merge(h.pruneWith(h.entries, h.current), entry)
	}
	h.selectView()
}

// merge takes already validated entries. Recheck time and quota after adding
// the new entry, but do not screen the existing text again.
func (h *replHistory) merge(entries []historyEntry, entry historyEntry) []historyEntry {
	if !h.valid(entry, h.now()) {
		return entries
	}
	return h.pruneWith(h.add(entries, entry), h.current)
}

func (h *replHistory) add(entries []historyEntry, entry historyEntry) []historyEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Scope == entry.Scope {
			if entries[i].Text == entry.Text {
				return entries
			}
			break
		}
	}
	return append(entries, entry)
}

// update never republishes cached entries: only this invocation's new entry is
// merged with freshly locked disk state. A stale reader cannot undo a clear.
func (h *replHistory) update(entry *historyEntry, clear string) error {
	if h.path == "" {
		return errors.New("history storage disabled")
	}
	parent := filepath.Dir(h.path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	dir, err := os.Lstat(parent)
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o077 != 0 {
		return errors.New("unsafe history directory")
	}
	if stat, ok := dir.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe history directory owner")
	}
	lock, err := historyOpen(h.path+".lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN || time.Now().After(deadline) {
			return errors.New("history lock unavailable")
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var original []byte
	file, err := historyOpen(h.path, false)
	if err == nil {
		original, err = io.ReadAll(file)
		file.Close()
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var entries []historyEntry
	legacy := len(original) != 0 && !bytes.HasPrefix(original, []byte(historyHeader))
	if legacy && bytes.HasPrefix(original, []byte("kash-history-")) {
		return errors.New("unsupported history version")
	}
	if !legacy && len(original) != 0 {
		for line := range bytes.SplitSeq(original[len(historyHeader):], []byte{'\n'}) {
			var record historyEntry
			if len(line) <= h.maxBytes && json.Unmarshal(line, &record) == nil {
				entries = append(entries, record)
			}
		}
	}
	known := make(map[string]int, len(h.entries))
	for i := range h.entries {
		known[h.entries[i].ID] = i
	}
	entries = h.pruneWith(entries, func(record historyEntry, now time.Time) bool {
		// The freshly read record must match every field before reusing screening.
		if i, ok := known[record.ID]; ok && h.entries[i] == record {
			return h.current(record, now)
		}
		return h.valid(record, now)
	})
	if clear != "" {
		entries = slices.DeleteFunc(entries, func(e historyEntry) bool {
			return clear == "global" || e.Scope == h.scope
		})
	}
	if entry != nil {
		entries = h.merge(entries, *entry)
	}
	var data bytes.Buffer
	data.WriteString(historyHeader)
	for _, record := range entries {
		data.Write(historyRecord(record))
	}
	if !bytes.Equal(original, data.Bytes()) {
		if err := replacePrivateFile(h.path, data.Bytes()); err != nil {
			return err
		}
	}
	h.entries = entries
	if legacy && h.out != nil {
		fmt.Fprintln(h.out, "kash: history: discarded untimestamped legacy history")
	}
	return nil
}

// O_NOFOLLOW and O_NONBLOCK avoid following final-component symlinks or
// blocking on a substituted FIFO. Reject hard links and files owned by others.
func historyOpen(path string, create bool) (*os.File, error) {
	flags := syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT
	}
	fd, err := syscall.Open(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	var stat syscall.Stat_t
	if err = syscall.Fstat(fd, &stat); err != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFREG ||
		stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		file.Close()
		return nil, errors.New("unsafe history file")
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// readline must not apply its own deduplication/cap policy when recording.
func (h *replHistory) Write(string) (int, error) { return h.Len(), nil }

func (h *replHistory) GetLine(index int) (string, error) {
	if index < 0 || index >= len(h.view) {
		return "", errors.New("history index out of range")
	}
	// Views contain validated entries; only time can invalidate a slot while editing.
	if h.disabled || !h.current(h.view[index], h.now()) {
		return "", nil
	}
	return h.view[index].Text, nil
}

func (h *replHistory) Len() int { return len(h.view) }

// Description annotates a recall result without changing its command text.
func (h *replHistory) Description(index int) string {
	if line, _ := h.GetLine(index); line == "" {
		return ""
	}
	return h.view[index].Time.Local().Format("2006-01-02 15:04:05 MST")
}

func (h *replHistory) expireView() bool {
	changed := false
	now := h.now()
	if len(h.view) == 0 || h.expireAt.IsZero() ||
		now.Before(h.expireAt) && !now.Before(h.view[len(h.view)-1].Time) {
		return false
	}
	h.expireAt = time.Time{}
	for i := range h.view {
		if h.view[i].Text != "" && !h.current(h.view[i], now) {
			h.view[i].Text = ""
			changed = true
		}
		if h.view[i].Text != "" {
			deadline := h.view[i].Time.Add(h.maxAge)
			if h.expireAt.IsZero() || deadline.Before(h.expireAt) {
				h.expireAt = deadline
			}
		}
	}
	if changed {
		h.entries = h.prune(h.entries)
	}
	return changed
}

func (h *replHistory) Dump() any {
	var lines []string
	for i := range h.view {
		if line, _ := h.GetLine(i); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (h *replHistory) Close() {}

func (h *replHistory) command(text string, out, errOut io.Writer) (bool, int) {
	fields := strings.Fields(text)
	if len(fields) == 0 || fields[0] != "history" || strings.ContainsAny(text, "\n;|&") {
		return false, 0
	}
	if len(fields) == 2 && fields[1] == "scope" {
		view := "local"
		if h.global {
			view = "global"
		}
		fmt.Fprintf(out, "history scope %s %q\n", view, h.scope.Root)
		return true, 0
	}
	if len(fields) == 3 && (fields[2] == "local" || fields[2] == "global") {
		switch fields[1] {
		case "scope":
			h.setScope(fields[2] == "global")
			h.selectView()
			return true, 0
		case "clear":
			if h.disabled {
				return true, 0
			}
			err := h.update(nil, fields[2])
			h.entries = slices.DeleteFunc(h.entries, func(e historyEntry) bool {
				return fields[2] == "global" || e.Scope == h.scope
			})
			h.selectView()
			if err != nil {
				fmt.Fprintln(errOut, "kash: history: could not clear persistent history")
				return true, 1
			}
			return true, 0
		}
	}
	fmt.Fprintln(errOut, "history: expected scope [local|global] or clear local|global")
	return true, 1
}
