package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"kame/lang/usage"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Command completion specs (spec 058) are Usage KDL files built by an
// embedded Kame program in a private build root. Tab only reads them; a
// background kame child refreshes them.
type completionSpecs struct {
	root                         string
	usageDirs, fishDirs, manDirs []string
	userRules                    string
	executable                   string // kame for builds; empty means this executable
	wakeEditor                   func() // configured before editing starts

	mu        sync.Mutex
	parsed    map[string]parsedSpec
	queued    map[string]bool
	pending   map[string]chan struct{}
	running   bool
	refreshed map[string]time.Time
	live      map[string]string // executable path -> cobra or clap
}

type parsedSpec struct {
	modTime time.Time
	size    int64
	spec    usage.Spec
}

const (
	specBudget        = 100 * time.Millisecond // a Tab waits this long for a missing spec
	specRefreshPeriod = 10 * time.Minute       // entries refresh a command at most this often
	maxSpecBytes      = 16 << 20
)

// specName is a command name that can name a spec file and a Kame target.
var specName = regexp.MustCompile(`^[A-Za-z0-9_+][A-Za-z0-9_.+-]*$`)

// newCompletionSpecs fixes discovery roots from the invocation environment.
// It returns nil when no cache directory can be determined.
func newCompletionSpecs() *completionSpecs {
	home, _ := os.UserHomeDir()
	xdg := func(name, fallback string) string {
		if dir := os.Getenv(name); filepath.IsAbs(dir) {
			return dir
		}
		if home == "" {
			return ""
		}
		return filepath.Join(home, fallback)
	}
	config, cache := xdg("XDG_CONFIG_HOME", ".config"), xdg("XDG_CACHE_HOME", ".cache")
	if cache == "" {
		return nil
	}
	data := absoluteDirs(os.Getenv("XDG_DATA_DIRS"), []string{"/usr/local/share", "/usr/share"})
	c := specStore(filepath.Join(cache, "kash", "completions"))
	if config != "" {
		c.usageDirs = append(c.usageDirs, filepath.Join(config, "kash", "completions"))
		c.fishDirs = append(c.fishDirs, filepath.Join(config, "fish", "completions"))
		c.userRules = filepath.Join(config, "kash", "completions.km")
	}
	for _, dir := range data {
		c.usageDirs = append(c.usageDirs, filepath.Join(dir, "kash", "completions"))
		c.fishDirs = append(c.fishDirs, filepath.Join(dir, "fish", "vendor_completions.d"), filepath.Join(dir, "fish", "completions"))
	}
	c.fishDirs = append(c.fishDirs, filepath.Join(cache, "fish", "generated_completions"))
	defaults := []string{"/usr/local/share/man", "/usr/share/man"}
	if manpath, ok := os.LookupEnv("MANPATH"); ok {
		var dirs []string
		for _, dir := range strings.Split(manpath, ":") {
			if dir == "" {
				dirs = append(dirs, defaults...)
			} else if filepath.IsAbs(dir) {
				dirs = append(dirs, dir)
			}
		}
		defaults = dirs
	}
	c.manDirs = unique(defaults)
	return c
}

func specStore(root string) *completionSpecs {
	return &completionSpecs{root: root, parsed: map[string]parsedSpec{}, queued: map[string]bool{}, pending: map[string]chan struct{}{}, refreshed: map[string]time.Time{}, live: map[string]string{}}
}

func absoluteDirs(list string, fallback []string) []string {
	var dirs []string
	for _, dir := range strings.Split(list, ":") {
		if filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return fallback
	}
	return unique(dirs)
}

func unique(items []string) []string {
	var out []string
	for _, item := range items {
		if !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out
}

// lookup returns the spec for a command: the cached build product, else a
// hand-written Usage file. cached reports whether the build product exists.
func (c *completionSpecs) lookup(name string) (spec usage.Spec, found, cached bool) {
	if spec, ok := c.read(c.specPath(name)); ok {
		return spec, true, true
	}
	for _, dir := range c.usageDirs {
		if spec, ok := c.read(filepath.Join(dir, name+".usage.kdl")); ok {
			return spec, true, false
		}
	}
	return usage.Spec{}, false, false
}

func (c *completionSpecs) specPath(name string) string {
	return filepath.Join(c.root, "specs", name+".usage.kdl")
}

func (c *completionSpecs) read(path string) (usage.Spec, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSpecBytes {
		return usage.Spec{}, false
	}
	c.mu.Lock()
	entry, ok := c.parsed[path]
	c.mu.Unlock()
	if ok && entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() {
		return entry.spec, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return usage.Spec{}, false
	}
	spec, err := usage.ParseKDL(string(data))
	if err != nil {
		return usage.Spec{}, false
	}
	c.mu.Lock()
	c.parsed[path] = parsedSpec{modTime: info.ModTime(), size: info.Size(), spec: spec}
	c.mu.Unlock()
	return spec, true
}

// refresh queues a background build or joins one already pending for name.
// A recently refreshed spec needs no build and returns nil.
func (c *completionSpecs) refresh(name string) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queueRefresh(name)
}

// queueRefresh requires c.mu.
func (c *completionSpecs) queueRefresh(name string) <-chan struct{} {
	if done := c.pending[name]; done != nil {
		return done
	}
	if time.Since(c.refreshed[name]) < specRefreshPeriod {
		return nil
	}
	done := make(chan struct{})
	c.pending[name] = done
	c.queued[name] = true
	c.refreshed[name] = time.Now()
	if !c.running {
		c.running = true
		go c.drain()
	}
	return done
}

// await refreshes a missing spec and waits for it up to the completion
// budget, including when typing already queued the build.
func (c *completionSpecs) await(name string) {
	done := c.refresh(name)
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(specBudget):
	}
}

// refreshUsed refreshes commands an entry used, unless refreshed recently.
func (c *completionSpecs) refreshUsed(names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range names {
		c.queueRefresh(name)
	}
}

// drain runs one build at a time; requests made meanwhile join the next.
func (c *completionSpecs) drain() {
	for {
		c.mu.Lock()
		if len(c.queued) == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}
		names := make([]string, 0, len(c.queued))
		for name := range c.queued {
			names = append(names, name)
		}
		c.queued = map[string]bool{}
		c.mu.Unlock()
		c.build(names, io.Discard, time.Minute)
		c.mu.Lock()
		for _, name := range names {
			close(c.pending[name])
			delete(c.pending, name)
		}
		c.mu.Unlock()
		if c.wakeEditor != nil {
			c.wakeEditor()
		}
	}
}

// build runs the completion program for names in a kame child process with
// no terminal, returning its exit status.
func (c *completionSpecs) build(names []string, out io.Writer, timeout time.Duration) int {
	slices.Sort(names)
	var args []string
	for _, name := range names {
		args = append(args, "./specs/"+name+".usage.kdl")
	}
	return c.run(nil, args, out, out, timeout)
}

func (c *completionSpecs) run(command, targets []string, out, errOut io.Writer, timeout time.Duration) int {
	if err := c.writeProgram(); err != nil {
		fmt.Fprintln(errOut, "complete: cannot prepare the completion cache:", err)
		return 1
	}
	exe, err := c.executable, error(nil)
	if exe == "" {
		exe, err = os.Executable()
	}
	if err != nil {
		fmt.Fprintln(errOut, "complete: cannot locate kame:", err)
		return 1
	}
	args := append([]string{"kame"}, command...)
	args = append(args, "-C", c.root, "--allow-read", "--allow-run", "--color", "never", "-o", "text",
		"-j", strconv.Itoa(runtime.NumCPU()), "--timeout", strconv.FormatInt(timeout.Milliseconds(), 10))
	args = append(args, targets...)
	// argv[0] is kame, not kash, so the child runs a build.
	cmd := &exec.Cmd{Path: exe, Args: args, Stdout: out, Stderr: errOut, SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(errOut, "complete:", err)
		return 1
	}
	return 0
}

// writeProgram replaces the build root's Kamefile when its text changed.
func (c *completionSpecs) writeProgram() error {
	if err := os.MkdirAll(filepath.Join(c.root, "user"), 0o700); err != nil {
		return err
	}
	path, text := filepath.Join(c.root, "Kamefile"), c.program()
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, []byte(text)) {
		return nil
	}
	return replacePrivateFile(path, []byte(text))
}

func (c *completionSpecs) program() string {
	var b strings.Builder
	b.WriteString("# Kash command completion specs (spec 058), written by kash: edits are replaced.\n")
	// Any change to this text rebuilds every spec, including a new parser.
	fmt.Fprintf(&b, "# kame %s (%s)\n", version, buildID)
	if c.userRules != "" {
		fmt.Fprintf(&b, "include? %s\n", kamePathLiteral(c.userRules))
	}
	for _, list := range []struct {
		name string
		dirs []string
	}{{"usage-dirs", c.usageDirs}, {"fish-dirs", c.fishDirs}, {"man-dirs", c.manDirs}} {
		quoted := make([]string, len(list.dirs))
		for i, dir := range list.dirs {
			quoted[i] = kameString(dir)
		}
		fmt.Fprintf(&b, "%s = [%s]\n", list.name, strings.Join(quoted, ", "))
	}
	b.WriteString(completionProgram)
	return b.String()
}

// kamePathLiteral writes an absolute path as a Kame path literal, escaping
// every byte that could end it or act as a pattern.
func kamePathLiteral(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		c := path[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("/._-+", c) >= 0) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// completionProgram merges, highest first: a hand-written Usage file, the
// output of a user rule, a fish file, and man pages, including NAME-SUB(1)
// pages whose synopsis starts with "NAME SUB". Expression bodies cannot run
// processes; only user rules can.
const completionProgram = `
def first-existing paths = paths |> filter exists? |> first
def compressed path = [path, "${path}.gz", "${path}.bz2"]
def usage-path name = first-existing (usage-dirs |> map (dir => joinpath dir "${name}.usage.kdl"))
def user-path name = first-existing ["./user/${name}.usage.kdl"]
def fish-path name = first-existing (fish-dirs |> map (dir => joinpath dir "${name}.fish"))

def read-usage path = if path then parse-usage (read path) else nil
def read-fish name path = if path then parse-fish-completions name (read path) else nil

def man-page name = first-existing (man-dirs |> flatmap (dir => ["1", "8"] |> flatmap (section => compressed (joinpath dir "man${section}" "${name}.${section}"))))
def man-spec path = parse-man (man-follow path (read path))
def man-follow path page = man-read page (man-target path (man-redirect page))
def man-target path target = if target and not (includes? ".." target) then first-existing (compressed (joinpath (dirname (dirname path)) target)) else nil
def man-read page target = if target then read target else page
def page-spec name = if man-page name then man-spec (man-page name) else nil

def sub-name name path = (capture (pattern "${name}-{sub:*}.{section:*}") (basename path)).sub ?? nil
def sub-pages name = man-dirs |> flatmap (dir => wildcard ${dir}/man1/${name}-*.1*)
def sub-spec name path = sub-check name (sub-name name path) (man-spec path)
def sub-check name sub spec = if sub and (spec.name ?? nil) == "${name} ${sub}" then put "name" sub spec else nil
def man-commands name = sub-pages name |> map (path => sub-spec name path) |> filter (spec => spec)
def man-tree name = merge-usage [page-spec name, {commands: man-commands name}]

completion-candidates = []
def completion-has-source name = (usage-path name) or (fish-path name) or (man-page name) or (user-path name) or (not (empty? (man-commands name)))
rule ./{batch:.eligible-*}/names.json = json (filter completion-has-source completion-candidates)

rule ./specs/{name}.usage.kdl =
	render-usage (put "name" name (merge-usage [(read-usage (usage-path name)), (read-usage (user-path name)), (read-fish name (fish-path name)), (man-tree name)]))
`

// eligible asks the builder which executables have sources, including user
// rule outputs not written yet and validated man subcommand pages.
func (c *completionSpecs) eligible(ctx completionContext, errOut io.Writer, timeout time.Duration) ([]string, int) {
	var candidates, names []string
	for _, entry := range pathExecutables(ctx, specName.MatchString) {
		candidates = append(candidates, kameString(entry.Name()))
	}
	if len(candidates) == 0 {
		return nil, 0
	}
	if err := c.writeProgram(); err != nil {
		fmt.Fprintln(errOut, "complete: cannot prepare the completion cache:", err)
		return nil, 1
	}
	// Use an artifact rule, like the spec builder, when observing user outputs.
	// Separate paths keep simultaneous refreshes from reading each other's lists.
	dir, err := os.MkdirTemp(c.root, ".eligible-")
	if err != nil {
		fmt.Fprintln(errOut, "complete: cannot prepare command discovery:", err)
		return nil, 1
	}
	defer os.Remove(dir)
	defer os.Remove(filepath.Join(dir, "names.json"))
	target := "./" + filepath.Base(dir) + "/names.json"
	slices.Sort(candidates)
	for len(candidates) != 0 {
		// Leave room under the OS's per-argument limit.
		n, size := 0, 0
		for n < len(candidates) && size < 64<<10 {
			size += len(candidates[n]) + 2
			n++
		}
		var output bytes.Buffer
		status := c.run([]string{"do", "cat"}, []string{"--set", "completion-candidates=[" + strings.Join(candidates[:n], ", ") + "]", target}, &output, errOut, timeout)
		if status != 0 {
			return nil, status
		}
		var batch []string
		if err := json.Unmarshal(output.Bytes(), &batch); err != nil {
			fmt.Fprintln(errOut, "complete: cannot read eligible command names:", err)
			return nil, 1
		}
		for _, name := range batch {
			if !specName.MatchString(name) {
				fmt.Fprintf(errOut, "complete: invalid command name %q\n", name)
				return nil, 1
			}
		}
		names = append(names, batch...)
		candidates = candidates[n:]
	}
	return names, 0
}

// command runs the complete builtin: --refresh [--all | NAME...],
// --show NAME, or --live [cobra|clap PATH].
func (c *completionSpecs) command(args []string, ctx completionContext, out, errOut io.Writer) int {
	usageError := func() int {
		fmt.Fprintln(errOut, "complete: expected complete --refresh [--all | NAME...], --show NAME, or --live [cobra|clap PATH]")
		return 2
	}
	if len(args) == 0 {
		return usageError()
	}
	if c == nil {
		fmt.Fprintln(errOut, "complete: no cache directory")
		return 1
	}
	if args[0] == "--live" {
		return c.liveCommand(args[1:], out, errOut, usageError)
	}
	names := args[1:]
	for _, name := range names {
		if !specName.MatchString(name) && !(args[0] == "--refresh" && name == "--all" && len(names) == 1) {
			fmt.Fprintf(errOut, "complete: invalid command name %q\n", name)
			return 2
		}
	}
	switch {
	case args[0] == "--show" && len(names) == 1:
		spec, found, cached := c.lookup(names[0])
		if !found {
			fmt.Fprintf(errOut, "complete: no spec for %s; run complete --refresh %s\n", names[0], names[0])
			return 1
		}
		if cached {
			fmt.Fprintf(out, "// %s\n", c.specPath(names[0]))
		}
		io.WriteString(out, usage.RenderKDL(spec))
		return 0
	case args[0] == "--refresh" && len(names) != 0:
		timeout := time.Minute
		if names[0] == "--all" {
			timeout = 30 * time.Minute
			var status int
			names, status = c.eligible(ctx, errOut, timeout)
			if status != 0 {
				return status
			}
		}
		if len(names) == 0 {
			return 0
		}
		var log bytes.Buffer
		status := c.build(names, &log, timeout)
		if status != 0 {
			errOut.Write(log.Bytes())
		} else {
			fmt.Fprintf(out, "complete: refreshed %d command specs in %s\n", len(names), c.root)
		}
		c.mu.Lock()
		for _, name := range names {
			c.refreshed[name] = time.Now()
		}
		c.mu.Unlock()
		return status
	}
	return usageError()
}
