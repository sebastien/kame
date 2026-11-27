package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func historyFixture(t *testing.T) (*replHistory, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("KASH_HISTORY", filepath.Join(root, "state", "history"))
	t.Setenv("KASH_HISTORY_MAX_AGE_DAYS", "15")
	t.Setenv("KASH_HISTORY_MAX_BYTES", "1048576")
	t.Setenv("KASH_HISTORY_SCOPE", "local")
	h := loadReplHistory()
	h.out = &bytes.Buffer{}
	h.refresh(root)
	return h, root
}

func historyClone(h *replHistory) *replHistory {
	return &replHistory{path: h.path, maxAge: h.maxAge, maxBytes: h.maxBytes, now: h.now, out: &bytes.Buffer{}}
}

func TestHistorySecrets(t *testing.T) {
	for _, text := range []string{
		"TOKEN=abc", "let api-key = \"short\"", "set env/CLIENT_SECRET = \"x\"",
		"curl --password short", "curl --api-key=abc", `curl "--token=abc"`,
		`sh -c 'export AWS_SECRET_ACCESS_KEY=literal'`,
		`TOKEN='$TOKEN'`, `TOKEN=\$TOKEN`,
		`TOKEN=$TO\KEN`, `TOKEN=$TO"KEN"`, `TOKEN="$TO"KEN`,
		`echo ghp_abcdefghijklmnop"qrstuvwxyz"`,
		`curl '--token=$TOKEN'`,
		`curl 'https://user:${TOKEN}@example.org'`,
		`curl -H Authorization: Bearer '$TOKEN'`,
		"echo safe\n# password=literal", "echo safe\nTOKEN=literal",
		"cat << '-----BEGIN OPENSSH PRIVATE KEY-----'",
		"echo -----BEGIN ENCRYPTED PRIVATE KEY-----",
		"echo ghp_abcdefghijklmnopqrst1234567890",
		"echo github_pat_abcdefghijklmnopqrst_1234567890",
		"echo glpat-abcdefghijklmnopqrst",
		"echo xoxb-1234567890-abcdef",
		"echo xapp-1-abcdefghijklmnopqrst",
		"echo sk_live_abcdefghijklmnop",
		"echo sk-proj-abcdefghijklmnopqrst",
		"echo sk-ant-api03-abcdefghijklmnopqrst",
		"echo AKIAABCDEFGHIJKLMNOP",
		"echo eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.signature",
		"curl https://user:password@example.org",
		`curl -H "Authorization: Bearer secret"`,
		`curl -H 'Proxy-Authorization: Basic abcdef'`,
		`curl -H "Authorization: Bearer mixed-$TOKEN"`,
		`curl -H 'Authorization: Bearer $TOKEN'`,
		"tool --credential='unterminated",
	} {
		if !historySecret(text) {
			t.Errorf("credential form was not rejected: %q", text)
		}
	}
	for _, text := range []string{
		"echo ordinary", "sha256sum abcdef0123456789abcdef0123456789abcdef",
		"TOKEN=$TOKEN", `TOKEN="${TOKEN}"`, `curl --api-key "$API_KEY"`,
		`curl --password ""`, "TOKEN=''", "echo password",
		`curl -H "Authorization: Bearer $TOKEN"`,
		`curl -H "Proxy-Authorization: Basic ${AUTH}"`,
		"echo ghp_short", "curl https://example.org/path",
	} {
		if historySecret(text) {
			t.Errorf("noncredential form was rejected: %q", text)
		}
	}
}

func TestHistoryRoundTripAndPrivacy(t *testing.T) {
	h, cwd := historyFixture(t)
	for _, text := range []string{"echo one", "echo one", "echo α\n\techo β", " TOKEN=hidden", "echo ghp_abcdefghijklmnopqrst1234567890", "history", "history -g", "   "} {
		h.record(text)
	}
	if len(h.entries) != 2 {
		t.Fatalf("wanted two eligible entries, got %d", len(h.entries))
	}
	other := historyClone(h)
	other.refresh(cwd)
	if got, _ := other.GetLine(1); got != "echo α\n\techo β" {
		t.Fatalf("multiline round trip: %q", got)
	}
	data, err := os.ReadFile(h.path)
	if err != nil || bytes.Contains(data, []byte("hidden")) || bytes.Contains(data, []byte("ghp_")) {
		t.Fatal("history contains rejected content or could not be read")
	}
	for _, path := range []string{h.path, h.path + ".lock"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("history files must be private")
		}
	}
}

func TestHistoryCommand(t *testing.T) {
	h, root := historyFixture(t)
	h.record("echo one")
	h.record("echo two")
	var out, errOut bytes.Buffer
	if handled, status := h.command("history", &out, &errOut); !handled || status != 0 {
		t.Fatalf("bare history: handled=%v status=%d", handled, status)
	}
	want := "history: local " + fmt.Sprintf("%q", root) + "\necho one\necho two\n\n  history -g  show global history\n  Ctrl-R      search history\n  Ctrl-X r    pick from history\n"
	if out.String() != want {
		t.Fatalf("bare history output:\n%s\nwant:\n%s", out.String(), want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("bare history error: %q", errOut.String())
	}
	out.Reset()
	if _, status := h.command("history --global", &out, &errOut); status != 0 || !h.global {
		t.Fatal("--global did not select the global view")
	}
	if !strings.HasPrefix(out.String(), "history: global\n") || !strings.Contains(out.String(), "history -l  show local history") {
		t.Fatalf("global history output: %q", out.String())
	}
	out.Reset()
	if _, status := h.command("history -l", &out, &errOut); status != 0 || h.global {
		t.Fatal("-l did not select the local view")
	}
	if !strings.HasPrefix(out.String(), "history: local ") {
		t.Fatalf("local history output: %q", out.String())
	}
	out.Reset()
	if _, status := h.command("history -x", &out, &errOut); status != 1 {
		t.Fatal("unknown history flag was accepted")
	}
}

func TestHistoryExpiryAndRescan(t *testing.T) {
	h, cwd := historyFixture(t)
	now := h.now()
	h.record("echo kept")
	h.record("echo removed")
	entries := append([]historyEntry(nil), h.entries...)
	entries[0].Text = "TOKEN=stored-secret"
	entries[1].Time = now.Add(time.Second) // future timestamps fail closed
	var data bytes.Buffer
	data.WriteString(historyHeader)
	for _, entry := range entries {
		data.Write(historyRecord(entry))
	}
	if err := os.WriteFile(h.path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	h.refresh(cwd)
	if h.Len() != 0 {
		t.Fatal("stored secrets and future timestamps were not removed")
	}
	data.Reset()
	raw, _ := os.ReadFile(h.path)
	if bytes.Contains(raw, []byte("stored-secret")) {
		t.Fatal("rescan left secret on disk")
	}
	h.record("echo deadline")
	now = h.entries[0].Time
	h.now = func() time.Time { return now.Add(-time.Nanosecond) }
	if got, _ := h.GetLine(0); got != "" {
		t.Fatal("clock rollback made a future entry recallable")
	}
	h.now = func() time.Time { return now.Add(h.maxAge - time.Nanosecond) }
	if got, _ := h.GetLine(0); got == "" {
		t.Fatal("entry expired before deadline")
	}
	h.now = func() time.Time { return now.Add(h.maxAge) }
	if got, _ := h.GetLine(0); got != "" {
		t.Fatal("idle prompt recalled expired entry")
	}
	h.refresh(cwd)
	if h.Len() != 0 {
		t.Fatal("expired entry was retained")
	}
	h.now = func() time.Time { return now }
	h.record("echo older")
	now = now.Add(time.Hour)
	h.record("echo newer")
	now = now.Add(-time.Hour)
	h.refresh(cwd)
	if len(h.entries) != 1 || h.entries[0].Text != "echo older" || strings.Contains(nativeCLIRead(h.path), "echo newer") {
		t.Fatal("clock rollback kept a future entry when the oldest was still valid")
	}
}

func TestHistoryScopeAndQuota(t *testing.T) {
	h, root := historyFixture(t)
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "sub")
	nested := filepath.Join(sub, "nested")
	other := filepath.Join(root, "other")
	for _, path := range []string{sub, nested, other} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.refresh(sub)
	if h.scope != (historyScope{repo, "repository"}) {
		t.Fatal("worktree root was not selected")
	}
	h.record("cd ../other")
	if h.entries[0].CWD != sub || h.entries[0].Scope.Root != repo {
		t.Fatal("submission directory was not retained")
	}
	if err := os.Mkdir(filepath.Join(repo, ".jj"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, colocated := historyContext(sub)
	if colocated != h.scope {
		t.Fatal("co-located metadata split the scope")
	}
	if err := os.Mkdir(filepath.Join(nested, ".jj"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, scope := historyContext(nested)
	if scope.Root != nested {
		t.Fatal("nearest repository root was not selected")
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	cwd, scope := historyContext(link)
	if cwd != sub || scope != h.scope {
		t.Fatal("symlinked cwd was not canonicalized")
	}
	h.refresh(other)
	if h.Len() != 0 {
		t.Fatal("local recall crossed scopes")
	}
	h.record("echo other")
	h.command("history -g", &bytes.Buffer{}, &bytes.Buffer{})
	if h.Len() != 2 {
		t.Fatal("global recall did not combine scopes")
	}
	h.command("history --local", &bytes.Buffer{}, &bytes.Buffer{})
	if h.Len() != 1 {
		t.Fatal("local view was not restored")
	}
	// Exactly one encoded entry fits in each scope. A new record must evict
	// the previous local one without affecting another scope.
	h.maxBytes = len(historyRecord(h.entries[len(h.entries)-1]))
	h.record("echo newer")
	h.refresh(sub)
	if len(h.entries) != 1 { // repo's longer metadata exceeds the new budget
		t.Fatalf("unexpected per-scope quota result: %d", len(h.entries))
	}
	h.refresh(other)
	before := len(h.entries)
	h.record("echo " + strings.Repeat("x", h.maxBytes))
	if len(h.entries) != before {
		t.Fatal("oversized input evicted existing records")
	}
}

func TestHistoryQuotaIndependentScopes(t *testing.T) {
	h, root := historyFixture(t)
	now := h.now()
	var entries []historyEntry
	for i := 0; i < 4; i++ {
		cwd := filepath.Join(root, fmt.Sprint(i%2))
		entries = append(entries, historyEntry{
			ID: fmt.Sprintf("%032x", i+1), Time: now.Add(time.Duration(i-4) * time.Second),
			Text: "echo αβ", CWD: cwd, Scope: historyScope{cwd, "directory"},
		})
	}
	h.maxBytes = len(historyRecord(entries[0]))
	kept := h.pruneWith(entries, h.valid)
	if len(kept) != 2 || kept[0].ID != entries[2].ID || kept[1].ID != entries[3].ID {
		t.Fatal("quota did not retain newest record independently in each scope")
	}
}

func TestHistoryConcurrentAndClear(t *testing.T) {
	h, cwd := historyFixture(t)
	stale := historyClone(h)
	stale.refresh(cwd)
	h.record("echo first")
	stale.refresh(cwd)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			writer := historyClone(h)
			entry := historyEntry{ID: fmt.Sprintf("%032x", i+1), Time: h.now(), Text: fmt.Sprintf("echo writer-%d", i), CWD: h.cwd, Scope: h.scope}
			// The command path deliberately bounds lock acquisition. A busy
			// attempt may be retried before checking the committed history.
			deadline := time.Now().Add(3 * time.Second)
			for {
				err := writer.update(&entry, "")
				if err == nil || err.Error() != "history lock unavailable" || time.Now().After(deadline) {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	h.refresh(cwd)
	if len(h.entries) != 13 {
		t.Fatalf("concurrent additions lost: %d", len(h.entries))
	}
	if _, status := h.command("history clear global", &bytes.Buffer{}, &bytes.Buffer{}); status != 0 {
		t.Fatal("clear failed")
	}
	stale.record("echo after-clear")
	h.refresh(cwd)
	if len(h.entries) != 1 || h.entries[0].Text != "echo after-clear" {
		t.Fatal("stale session resurrected cleared entries")
	}
}

func TestHistoryMergeRejectsUnvalidatedEntry(t *testing.T) {
	h, _ := historyFixture(t)
	h.record("echo kept")
	entry := h.entries[0]
	entry.Text = "TOKEN=stored-secret"
	if err := h.update(&entry, ""); err != nil {
		t.Fatal(err)
	}
	if len(h.entries) != 1 || h.entries[0].Text != "echo kept" {
		t.Fatal("merge accepted an invalid entry or displaced valid history")
	}
}

func TestHistoryMigrationAndUnsafeStorage(t *testing.T) {
	h, cwd := historyFixture(t)
	if err := os.WriteFile(h.path, []byte("TOKEN=legacy-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.refresh(cwd)
	data, _ := os.ReadFile(h.path)
	if string(data) != historyHeader {
		t.Fatal("legacy history was not discarded")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(h.path), ".kash-*"))
	if len(matches) != 0 {
		t.Fatal("temporary history files leaked")
	}
	if err := os.WriteFile(h.path, []byte("kash-history-v999\nprivate-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.record("echo current")
	data, _ = os.ReadFile(h.path)
	if string(data) != "kash-history-v999\nprivate-data" {
		t.Fatal("unknown future format was destroyed")
	}
	if err := os.Remove(h.path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cwd, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, h.path); err != nil {
		t.Fatal(err)
	}
	if err := h.update(nil, ""); err == nil {
		t.Fatal("symlink target accepted")
	}
	data, _ = os.ReadFile(target)
	if string(data) != "untouched" {
		t.Fatal("symlink target changed")
	}
}

func TestHistoryLockAndConfiguration(t *testing.T) {
	h, cwd := historyFixture(t)
	h.record("echo persisted")
	lock, err := historyOpen(h.path+".lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h.record("echo memory-only")
	if time.Since(start) > time.Second || h.Len() != 2 {
		t.Fatal("lock contention blocked execution or lost session memory")
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	h.record("echo recovered")
	other := historyClone(h)
	other.refresh(cwd)
	if len(other.entries) != 2 || other.entries[0].Text != "echo persisted" || other.entries[1].Text != "echo recovered" {
		t.Fatal("recovery republished memory-only history or lost a persisted command")
	}
	for _, setting := range []struct{ name, value string }{
		{"KASH_HISTORY_MAX_AGE_DAYS", "0"},
		{"KASH_HISTORY_MAX_AGE_DAYS", "999999999999999999999999"},
		{"KASH_HISTORY_MAX_BYTES", "-1"},
		{"KASH_HISTORY_MAX_BYTES", ""},
		{"KASH_HISTORY_SCOPE", "unknown"},
		{"KASH_HISTORY", ""},
	} {
		t.Run(setting.name+setting.value, func(t *testing.T) {
			t.Setenv(setting.name, setting.value)
			disabled := loadReplHistory()
			disabled.refresh(cwd)
			disabled.record("echo must-not-record")
			if !disabled.disabled || disabled.Len() != 0 || len(disabled.entries) != 0 {
				t.Fatal("invalid configuration did not disable all history")
			}
		})
	}
}

func TestHistoryMalformedRecords(t *testing.T) {
	h, cwd := historyFixture(t)
	h.record("echo valid")
	valid := historyRecord(h.entries[0])
	invalid := h.entries[0]
	invalid.ID = "not-an-id"
	record, _ := json.Marshal(invalid)
	data := append([]byte(historyHeader+"not-json\n"), valid...)
	data = append(data, record...)
	data = append(data, '\n')
	if err := os.WriteFile(h.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	h.refresh(cwd)
	if h.Len() != 1 {
		t.Fatal("invalid record prevented recovery of valid record")
	}
	info, err := os.Stat(h.path)
	if err != nil {
		t.Fatal(err)
	}
	changed := h.entries[0]
	changed.Text = "TOKEN=abcd" // same length as "echo valid"
	if err := os.WriteFile(h.path, append([]byte(historyHeader), historyRecord(changed)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(h.path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	h.refresh(cwd)
	if data, err := os.ReadFile(h.path); err != nil || string(data) != historyHeader || h.Len() != 0 {
		t.Fatal("a disk edit bypassed screening by reusing a known entry ID")
	}
}

func TestHistoryPrivateStorageAndFailedReplacement(t *testing.T) {
	h, cwd := historyFixture(t)
	h.record("echo preserved")
	original, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	// A directory cannot be atomically replaced by a history file. Failure
	// must clean up the private temporary file and leave existing data alone.
	target := filepath.Join(filepath.Dir(h.path), "directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := replacePrivateFile(target, []byte("TOKEN=not-published")); err == nil {
		t.Fatal("replacement unexpectedly succeeded")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(h.path), ".kash-*"))
	if len(matches) != 0 {
		t.Fatal("failed replacement leaked temporary data")
	}
	after, _ := os.ReadFile(h.path)
	if !bytes.Equal(original, after) {
		t.Fatal("failed replacement changed existing history")
	}
	if err := os.Chmod(filepath.Dir(h.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.update(nil, ""); err == nil {
		t.Fatal("history accepted a publicly accessible parent directory")
	}
	info, _ := os.Stat(filepath.Dir(h.path))
	if info.Mode().Perm() != 0o755 {
		t.Fatal("history changed an existing parent directory's permissions")
	}
	h.refresh(cwd)
	if h.Len() != 1 {
		t.Fatal("storage failure disabled eligible session recall")
	}
}

func TestHistoryLocalClearAndDeduplication(t *testing.T) {
	h, root := historyFixture(t)
	a := filepath.Join(root, "other")
	if err := os.Mkdir(a, 0o700); err != nil {
		t.Fatal(err)
	}
	h.record("echo repeated")
	firstTime := h.entries[0].Time
	h.refresh(a)
	h.record("echo unrelated")
	h.refresh(root)
	h.record("echo repeated")
	if len(h.entries) != 2 || !h.entries[0].Time.Equal(firstTime) {
		t.Fatal("deduplication was not per owning scope or refreshed retention")
	}
	// Global selection must not change the meaning of clear local.
	h.command("history -g", &bytes.Buffer{}, &bytes.Buffer{})
	if _, status := h.command("history clear local", &bytes.Buffer{}, &bytes.Buffer{}); status != 0 {
		t.Fatal("local clear failed")
	}
	if len(h.entries) != 1 || h.entries[0].Scope.Root != a {
		t.Fatal("local clear affected another scope")
	}
	other := historyClone(h)
	other.refresh(a)
	if other.Len() != 1 {
		t.Fatal("other scope was not retained on disk")
	}
}

func TestHistoryEditorCacheAndToggle(t *testing.T) {
	h, cwd := historyFixture(t)
	t.Setenv("INPUTRC", "/dev/null")
	t.Setenv("TERM", "dumb")
	h.record("echo first")
	h.record("echo second")
	e := &editorReader{history: h}
	e.resetEditor()
	e.shell.History.Walk(1)
	e.shell.Line().Set([]rune("TOKEN=edited-secret")...)
	e.shell.History.Save()
	e.clearHistoryCache()
	h.command("history clear global", &bytes.Buffer{}, &bytes.Buffer{})
	h.record("echo fresh")
	e.shell.Line().Set()
	e.shell.History.Walk(1)
	if got := string(*e.shell.Line()); got != "echo fresh" {
		t.Fatal("readline cached history bypassed deletion")
	}
	e.shell.Line().Set([]rune("echo unfinished")...)
	e.shell.Cursor().Set(5)
	e.toggleHistory()
	if !h.global || string(*e.shell.Line()) != "echo unfinished" || e.shell.Cursor().Pos() != 5 {
		t.Fatal("scope toggle changed unsubmitted input")
	}
	e.toggleHistory()
	if h.global || h.scope.Root != cwd {
		t.Fatal("local scope was not restored")
	}
	// Populate readline's edited-line cache, then cross the deadline at an
	// idle prompt. The command guard must flush that cache before navigating.
	e.shell.Line().Set()
	e.shell.History.Walk(1)
	e.shell.Line().Set([]rune("echo edited")...)
	e.shell.History.Save()
	now := h.now()
	h.now = func() time.Time { return now.Add(h.maxAge) }
	e.shell.Line().Set()
	e.shell.Keymap.Commands()["previous-history"]()
	if got := string(*e.shell.Line()); got != "" {
		t.Fatal("readline cached history bypassed expiration")
	}
	h.refresh(cwd)
	if nativeCLIRead(h.path) != historyHeader {
		t.Fatal("idle expiry left already-forgotten entries on disk")
	}
}
