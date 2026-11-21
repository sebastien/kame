package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These regressions exercise the compiled CLI and real signal/watch/process
// adapters, not the Go extern doubles.
func nativeCLIBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("KAME_NATIVE_CLI_TEST_BINARY")
	if binary == "" {
		t.Skip("set KAME_NATIVE_CLI_TEST_BINARY to run native execution regressions")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("KAME_NATIVE_CLI_TEST_BINARY must be absolute")
	}
	return binary
}

func nativeCLIWrite(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func nativeCLIRead(name string) string {
	data, _ := os.ReadFile(name)
	return string(data)
}

func nativeCLIStart(t *testing.T, directory string, args ...string) (*exec.Cmd, string, string) {
	t.Helper()
	stdout, stderr := filepath.Join(directory, "stdout"), filepath.Join(directory, "stderr")
	out, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	errOut, err := os.Create(stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { errOut.Close() })
	command := exec.Command(nativeCLIBinary(t), args...)
	command.Dir, command.Stdout, command.Stderr = directory, out, errOut
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			command.Process.Signal(syscall.SIGTERM)
			nativeCLIWait(t, command, stderr) // Also bounds cleanup when a regression hangs.
		}
		text := nativeCLIRead(stderr)
		if strings.Contains(text, "AddressSanitizer") || strings.Contains(text, "runtime error:") || strings.Contains(text, "LeakSanitizer") {
			t.Errorf("native runtime check failed:\n%s", text)
		}
	})
	return command, stdout, stderr
}

func nativeCLIWait(t *testing.T, command *exec.Cmd, stderr string) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-done:
		return command.ProcessState.ExitCode()
	case <-time.After(5 * time.Second):
		command.Process.Kill()
		<-done
		t.Fatalf("native CLI did not exit:\n%s", nativeCLIRead(stderr))
		return -1
	}
}

func nativeCLIAwait(t *testing.T, stderr string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("native CLI handshake timed out:\n%s", nativeCLIRead(stderr))
}

func TestNativeCatSignalsCancelAndReap(t *testing.T) {
	nativeCLIBinary(t)
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		for _, repeated := range []bool{false, true} {
			t.Run(fmt.Sprintf("signal-%d/repeated-%t", signal, repeated), func(t *testing.T) {
				directory := t.TempDir()
				// The TERM trap acknowledges graceful shutdown while the recipe
				// remains active. Send the second signal only after this handshake.
				nativeCLIWrite(t, filepath.Join(directory, "Makefile.km"), `SHELL = "/bin/sh"
rule ./artifact
	trap 'touch cancelling; while :; do sleep .01; done' TERM
	echo $$ > leader
	sleep 30 &
	echo $! > child
	touch ready
	wait
	printf leaked > "${outputs.0}"
`)
				args := []string{"do", "cat", "./artifact"}
				if repeated {
					args = append(args, "--json")
				}
				command, stdout, stderr := nativeCLIStart(t, directory, args...)
				t.Cleanup(func() {
					// A broken cancellation loop must not leave this test's recipe
					// group running after the bounded wait kills the CLI itself.
					pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "leader"))))
					if err == nil {
						syscall.Kill(-pid, syscall.SIGKILL)
					}
				})
				nativeCLIAwait(t, stderr, func() bool {
					return nativeCLIRead(filepath.Join(directory, "child")) != "" && fileExists(filepath.Join(directory, "ready"))
				})
				if err := command.Process.Signal(signal); err != nil {
					t.Fatal(err)
				}
				want := 1
				if repeated {
					deadline := time.Now().Add(5 * time.Second)
					for !fileExists(filepath.Join(directory, "cancelling")) {
						if time.Now().After(deadline) {
							t.Fatalf("cat did not begin cancellation: %s", nativeCLIRead(stderr))
						}
						time.Sleep(time.Millisecond)
					}
					if err := command.Process.Signal(signal); err != nil {
						t.Fatal(err)
					}
					want = 128 + int(signal)
				}
				if status := nativeCLIWait(t, command, stderr); status != want {
					t.Fatalf("status %d, want %d: %s", status, want, nativeCLIRead(stderr))
				}
				text := nativeCLIRead(stdout)
				if repeated {
					for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
						var event struct {
							Type string `json:"type"`
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil || (event.Type != "invocation-started" && event.Type != "summary") {
							t.Errorf("cancelled cat published data: %s (%v)", line, err)
						}
					}
					text = "" // JSON invocation framing is not artifact output.
				}
				if text != "" || fileExists(filepath.Join(directory, "artifact")) {
					t.Fatalf("cancelled cat published output: %q", text)
				}
				for _, name := range []string{"leader", "child"} {
					pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, name))))
					if err != nil {
						t.Fatal(err)
					}
					// Group SIGKILL is asynchronous for descendants, which the system
					// reaper owns. Wait for death without relaxing leader reaping.
					if name == "child" {
						nativeCLIAwait(t, stderr, func() bool {
							return syscall.Kill(pid, 0) == syscall.ESRCH ||
								strings.Contains(nativeCLIRead(fmt.Sprintf("/proc/%d/status", pid)), "State:\tZ")
						})
						continue
					}
					err = syscall.Kill(pid, 0)
					if err != syscall.ESRCH {
						t.Errorf("%s %d survived cancellation (kill check: %v)", name, pid, err)
						syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
		}
	}
}

func TestNativeKashSymlinkRunsInterpreterSourceAndArguments(t *testing.T) {
	binary := nativeCLIBinary(t)
	directory := t.TempDir()
	interpreter := filepath.Join(directory, "kash")
	if err := os.Symlink(binary, interpreter); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(directory, "script"), `printf "<%s>" $args`)
	command := exec.Command(interpreter, "script", "one", "two words")
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "<one><two words>" {
		t.Fatalf("kash symlink status=%v output=%q", err, output)
	}
	command = exec.Command(interpreter)
	command.Dir = directory
	command.Stdin = strings.NewReader("echo stdin\n")
	output, err = command.CombinedOutput()
	if err != nil || string(output) != "stdin\n" {
		t.Fatalf("kash stdin status=%v output=%q", err, output)
	}
}

// The block reads shared record fields without changing the outer binding.
func TestNativeKashSharedBindingsSurviveWithBlock(t *testing.T) {
	binary := nativeCLIBinary(t)
	directory := t.TempDir()
	interpreter := filepath.Join(directory, "kash")
	if err := os.Symlink(binary, interpreter); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(directory, "script"), "def r = {a: [\"x\", \"y\"], b: \"z\"}\nwith\n\techo (count r.a)\necho (count r.a) (get \"b\" r) (count (get \"a\" r))\n")
	command := exec.Command(interpreter, "script")
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "2\n2 z 2\n" {
		t.Fatalf("status=%v output=%q", err, output)
	}
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

func TestNativeMaterializeSignalsDuringServiceTeardown(t *testing.T) {
	nativeCLIBinary(t)
	for _, mode := range []string{"normal", "int", "term", "int-repeated", "term-repeated"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			// TERM announces final dependency release, then waits for the test
			// to permit graceful exit. Signals therefore arrive after the consumer
			// completed, not while materializeTargets still owns its active root.
			nativeCLIWrite(t, filepath.Join(directory, "Makefile.km"), "SHELL = \"/bin/sh\"\nservice daemon ; {ready: {argv: [\"test\", \"-f\", \"ready\"], interval-ms: 10, timeout-ms: 3000}, stop: {grace-ms: 3000}}\n\ttrap 'touch stopping; while [ ! -f release ]; do sleep .01; done; touch graceful; exit 0' TERM; echo $$ > service.pid; touch ready; while :; do sleep .01; done\ntask consumer : daemon\n\ttouch consumed\n")
			command, stdout, stderr := nativeCLIStart(t, directory, "--json", "consumer")
			t.Cleanup(func() {
				pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "service.pid"))))
				if err == nil {
					syscall.Kill(-pid, syscall.SIGKILL)
				}
			})
			nativeCLIAwait(t, stderr, func() bool {
				return fileExists(filepath.Join(directory, "stopping")) && fileExists(filepath.Join(directory, "consumed")) && strings.Contains(nativeCLIRead(stdout), `"type":"target-completed","target":"consumer"`)
			})
			want := 0
			repeated := strings.HasSuffix(mode, "-repeated")
			if mode != "normal" {
				signal := syscall.SIGINT
				if strings.HasPrefix(mode, "term") {
					signal = syscall.SIGTERM
				}
				if err := command.Process.Signal(signal); err != nil {
					t.Fatal(err)
				}
				time.Sleep(30 * time.Millisecond)
				want = 1
				if repeated {
					if err := command.Process.Signal(signal); err != nil {
						t.Fatal(err)
					}
					want = 128 + int(signal)
				}
			}
			if !repeated {
				nativeCLIWrite(t, filepath.Join(directory, "release"), "")
			}
			shutdownStarted := time.Now()
			if status := nativeCLIWait(t, command, stderr); status != want {
				t.Errorf("late-signal status %d, want %d:\n%s\n%s", status, want, nativeCLIRead(stdout), nativeCLIRead(stderr))
			}
			if repeated && time.Since(shutdownStarted) >= 2*time.Second {
				t.Error("repeated signal waited for the service's 3-second grace timeout")
			}
			if !repeated && !fileExists(filepath.Join(directory, "graceful")) {
				t.Error("service lost its graceful shutdown")
			}
			pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "service.pid"))))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Errorf("service leader %d was not reaped: %v", pid, err)
			}
		})
	}
}

func TestNativeWatchMissingMandatoryIncludeRecovers(t *testing.T) {
	nativeCLIBinary(t)
	for _, mode := range []string{"initial", "nested", "reload"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			root := filepath.Join(directory, "Makefile.km")
			missing := filepath.Join(directory, "missing.km")
			source := "include ./missing.km\nSHELL = \"/bin/sh\"\ntask chosen\n\tprintf %s @{MARK} > recovered\n"
			if mode == "nested" {
				nativeCLIWrite(t, filepath.Join(directory, "parent.km"), "include ./missing.km\n")
				source = strings.Replace(source, "./missing.km", "./parent.km", 1)
			}
			if mode == "reload" {
				nativeCLIWrite(t, root, "SHELL = \"/bin/sh\"\ntask chosen\n\tprintf initial > recovered\n")
			} else {
				nativeCLIWrite(t, root, source)
			}
			command, _, stderr := nativeCLIStart(t, directory, "--watch", "chosen")
			if mode == "reload" {
				nativeCLIAwait(t, stderr, func() bool {
					return nativeCLIRead(filepath.Join(directory, "recovered")) == "initial" && strings.Contains(nativeCLIRead(stderr), "waiting for changes")
				})
				nativeCLIWrite(t, root, source)
			}
			nativeCLIAwait(t, stderr, func() bool {
				text := nativeCLIRead(stderr)
				return strings.Contains(text, "FS_ERR") && strings.Contains(text, "cannot read included source: missing.km") && strings.Contains(text, "last cycle failure")
			})
			// Only the failed dependency changes; the parent source stays untouched.
			nativeCLIWrite(t, missing, "MARK = \"recovered\"\n")
			nativeCLIAwait(t, stderr, func() bool { return nativeCLIRead(filepath.Join(directory, "recovered")) == "recovered" })
			if err := command.Process.Signal(syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
			if status := nativeCLIWait(t, command, stderr); status != 130 {
				t.Fatalf("watch status %d: %s", status, nativeCLIRead(stderr))
			}
		})
	}
}

func TestNativeMaterializeDiagnosticsKeepOperandIdentity(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.km"), "task broken\n\texit 7\ntask other\n\texit 9\n")
	command, stdout, stderr := nativeCLIStart(t, directory, "--json", "missing", "broken", "other")
	if status := nativeCLIWait(t, command, stderr); status != 1 {
		t.Fatalf("status %d: %s", status, nativeCLIRead(stderr))
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(nativeCLIRead(stdout), "\n") {
		if line == "" {
			continue
		}
		var event struct {
			Type       string `json:"type"`
			Diagnostic struct {
				Code        string   `json:"code"`
				Target      string   `json:"target"`
				TargetStack []string `json:"targetStack"`
			} `json:"diagnostic"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "diagnostic" {
			continue
		}
		d := event.Diagnostic
		if d.Code == "" || len(d.TargetStack) == 0 || d.TargetStack[0] != d.Target {
			t.Errorf("wrong operand attribution: %s", line)
		}
		seen[d.Target] = true
	}
	for _, target := range []string{"missing", "broken", "other"} {
		if !seen[target] {
			t.Errorf("missing diagnostic for %s:\n%s", target, nativeCLIRead(stdout))
		}
	}
}

// Interactive Kash keeps one session across entries: failures end only their
// entry, eager bindings and functions persist and may be rebound, rejected
// entries leave the session unchanged, and exit ends the session.
func TestNativeInteractiveKashSession(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "lib.kash"), "let greeting = \"hello\"\n")
	entries := strings.Join([]string{
		"echo $greeting",
		"ls ./missing-entry",
		"echo after-failure",
		"def shout name",
		"\techo (uppercase name)",
		"",
		"(shout \"bob\")",
		"let greeting = \"rebound\"",
		"echo $greeting",
		"def lazy = 1",
		"def lazy = 2",
		"(nop lazy)",
		"echo (map",
		"  uppercase [\"a\", \"b\"])",
		"exit 3",
		"echo unreachable",
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i", "lib.kash"}, strings.NewReader(entries), &out, &errOut)
	want := "hello\nafter-failure\nBOB\nrebound\n1\nA B\n"
	if status != 3 || out.String() != want {
		t.Fatalf("interactive session: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	for _, fragment := range []string{"<kash:2>:1:1", "lazy definition already exists in this session: lazy", "<kash:9>:1:5"} {
		if !strings.Contains(errOut.String(), fragment) {
			t.Fatalf("interactive diagnostics lack %q: %q", fragment, errOut.String())
		}
	}
	if strings.Contains(errOut.String(), "unreachable") {
		t.Fatal("entries after exit ran")
	}
}

// Entry contracts: comments are accepted, += is rejected without effects, a
// partial block at end of input runs, end of input reports the last entry's
// status, and -i cannot also read source from stdin.
func TestNativeInteractiveKashEntryContracts(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	entries := "# a comment\nlet xs = [1]\nxs += [2]\necho (count xs)\nif true\n\techo partial\n\tfalse"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	if status != 1 || out.String() != "1\npartial\n" {
		t.Fatalf("entry contracts: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "error[PARSE_ERR]") || !strings.Contains(errOut.String(), "<kash:3>") || strings.Contains(errOut.String(), "FEATURE_UNSUP") {
		t.Fatalf("entry diagnostics: %q", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLIIn(t, directory, []string{"do", "run", "-i", "-"}, strings.NewReader("echo x\n"), &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_CONFLICT") {
		t.Fatalf("stdin source conflict: status=%d err=%q", status, errOut.String())
	}
}

func TestNativeInteractiveKashIndependentSourceDiagnostics(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	for _, entry := range []struct {
		text, location string
	}{
		{"def broken ()\n\t(fail! \"old entry\")\n\n" + strings.Repeat("(nop ())\n", 32) + "(broken ())\n", "<kash:1>:2:10"},
		{"def one arg\n\treturn 0\n\n(one 1 2)\n", "<kash:2>:1:2"},
		{"(apply (x => x) [1, 2])\n", "<kash:1>:1:9"},
		{"def bad = (x => x)\n(nop bad)\n", "<kash:1>:1:11"},
		{"def older x\n\t(fail! \"old version\")\n\nlet alias = older\ndef older x\n\treturn 0\n\n(alias 1)\n", "<kash:1>:2:10"},
	} {
		var out, errOut bytes.Buffer
		status := runNativeCLIIn(t, t.TempDir(), []string{"do", "run", "-i"}, strings.NewReader(entry.text), &out, &errOut)
		if status == 0 || !strings.Contains(errOut.String(), entry.location) {
			t.Errorf("source diagnostics: status=%d want=%s err=%s", status, entry.location, errOut.String())
		}
	}
}

func TestNativeInteractiveKashReclaimedEntriesKeepDeferredWorkAndExplain(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	entries := "(fail! \"keep this source\")\ndefer (out! \"deferred\")\n" +
		strings.Repeat("(nop ())\n", 256) + "explain\n"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, t.TempDir(), []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	if status != 0 || out.String() != "deferred" {
		t.Fatalf("reclaimed entries: status=%d out=%q err=%s", status, out.String(), errOut.String())
	}
	if strings.Count(errOut.String(), "error[USER_ERROR]: keep this source") != 2 ||
		strings.Count(errOut.String(), "<kash:1>:1:2") < 2 {
		t.Fatalf("explain lost the original source: %s", errOut.String())
	}
}

// Explicit grants do not move with cd, unlike the default policy.
func TestNativeInteractiveKashExplicitGrants(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	sub := filepath.Join(directory, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, sub, []string{"do", "run", "-i", "--allow-write=.", "--allow-run"}, strings.NewReader("cd ..\necho no > ./denied.txt\necho continued\n"), &out, &errOut)
	if status != 0 || out.String() != "continued\n" || !strings.Contains(errOut.String(), "CAP_DENIED") {
		t.Fatalf("explicit grants: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "denied.txt")); err == nil {
		t.Fatal("redirection escaped an explicit grant")
	}
}

// The startup file runs as entries before the first one: definitions and
// session commands apply, diagnostics locate file positions, and a failing
// entry does not stop later ones. Empty KASH_INIT disables it; an unreadable
// explicit file is reported and the session continues.
func TestNativeInteractiveKashStartupFile(t *testing.T) {
	directory := t.TempDir()
	init := filepath.Join(directory, "init.kash")
	startupDir := filepath.Join(directory, "startup")
	if err := os.Mkdir(startupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, init, "# startup\nlet who = \"init\"\n\nset env/FROM_INIT = \"yes\"\n\ndef hello name\n\techo hello $name\n\nls ./missing-startup\necho after-failure\nalias greet = [\"echo\", \"alias-from-init\"]\ncd startup\n")
	run := func(value, entries string) (int, string, string) {
		t.Helper()
		t.Setenv("KASH_INIT", value)
		var out, errOut bytes.Buffer
		status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
		return status, out.String(), errOut.String()
	}
	status, out, errOut := run(init, "echo $who\n(hello \"x\")\nprintenv FROM_INIT\nls ./missing-entry\ngreet\npwd\n")
	resolved, err := filepath.EvalSymlinks(startupDir)
	if err != nil {
		t.Fatal(err)
	}
	if status != 0 || out != "after-failure\ninit\nhello x\nyes\nalias-from-init\n"+resolved+"\n" {
		t.Fatalf("startup session: status=%d out=%q err=%q", status, out, errOut)
	}
	if !strings.Contains(errOut, init+":9:1") || !strings.Contains(errOut, "<kash:4>:1:1") {
		t.Fatalf("startup diagnostics: %q", errOut)
	}
	if status, out, errOut = run("", "echo $who\n"); status == 0 || strings.Contains(out, "init") {
		t.Fatalf("empty KASH_INIT loaded the startup file: status=%d out=%q err=%q", status, out, errOut)
	}
	if status, out, errOut = run(filepath.Join(directory, "missing.kash"), "echo ok\n"); status != 0 || out != "ok\n" || !strings.Contains(errOut, "HOST_FAIL") {
		t.Fatalf("unreadable startup file: status=%d out=%q err=%q", status, out, errOut)
	}
}

// startInteractiveKash starts `do run -i` without a terminal, with entries
// written to the returned pipe and output collected in files.
func startInteractiveKash(t *testing.T, directory string) (*exec.Cmd, io.WriteCloser, string, string) {
	t.Helper()
	stdout, stderr := filepath.Join(directory, "stdout"), filepath.Join(directory, "stderr")
	out, err := os.Create(stdout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	errOut, err := os.Create(stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { errOut.Close() })
	command := exec.Command(nativeCLIBinary(t), "do", "run", "-i")
	command.Dir, command.Env, command.Stdout, command.Stderr = directory, append(os.Environ(), "KASH_INIT="), out, errOut
	in, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			command.Process.Signal(syscall.SIGKILL)
			command.Wait()
		}
	})
	return command, in, stdout, stderr
}

// An interrupt received by the session itself, here during a capture, cancels
// and reaps only the running entry: the session continues without a
// diagnostic or a terminal newline, and the entry status is 130.
func TestNativeInteractiveKashSessionInterrupt(t *testing.T) {
	for _, mode := range []string{"continue", "status"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			command, in, stdout, stderr := startInteractiveKash(t, directory)
			io.WriteString(in, "let x = $(sh -c 'echo $$ > child; sleep 30')\n")
			nativeCLIAwait(t, stderr, func() bool { return strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "child"))) != "" })
			if err := command.Process.Signal(syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
			want, wantStatus := "", 130
			if mode == "continue" {
				io.WriteString(in, "echo after\n")
				want, wantStatus = "after\n", 0
			}
			in.Close()
			if status := nativeCLIWait(t, command, stderr); status != wantStatus || nativeCLIRead(stdout) != want || nativeCLIRead(stderr) != "" {
				t.Fatalf("session interrupt: status=%d out=%q err=%q", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
			}
			pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "child"))))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); err == nil {
				t.Fatalf("interrupted capture %d was not reaped", pid)
			}
		})
	}
}

// A second interrupt while an interrupted entry settles forces shutdown: the
// session ends with 130, kills the process groups, and runs no later entry.
func TestNativeInteractiveKashForcedInterrupt(t *testing.T) {
	directory := t.TempDir()
	command, in, stdout, stderr := startInteractiveKash(t, directory)
	// The child ignores the graceful stop, so settling lasts its grace period.
	io.WriteString(in, "sh -c 'trap \"\" INT TERM; echo $$ > child; while :; do sleep 0.01; done'\n")
	nativeCLIAwait(t, stderr, func() bool { return strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "child"))) != "" })
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if status := nativeCLIWait(t, command, stderr); status != 130 {
		t.Fatalf("forced interrupt status %d: %q", status, nativeCLIRead(stderr))
	}
	io.WriteString(in, "echo after\n")
	in.Close()
	if nativeCLIRead(stdout) != "" {
		t.Fatalf("entries ran after forced shutdown: %q", nativeCLIRead(stdout))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "child"))))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("forced shutdown left %d running", pid)
	}
}

// Without a terminal, a final & line runs a still-open entry (a block) while
// later entries run, and the session joins it at end of input; a lone &
// line after a complete entry is ignored.
func TestNativeInteractiveKashBackgroundLineWithoutTerminal(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	entries := "if true\n\tsh -c 'sleep 0.3'\n\techo background-done\n&\necho foreground-first\n&\n"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	if status != 0 || out.String() != "foreground-first\nbackground-done\n" {
		t.Fatalf("background entry: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
}

func TestNativeInteractiveKashAsyncFailureReporting(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	for _, test := range []struct {
		name, entries   string
		status, reports int
	}{
		{"unhandled-await", "let job = (run! :async true \"/bin/false\")\n(await job)\n", 1, 1},
		{"awaited", "let job = (run! :async true \"/bin/false\")\n(await job)\n/bin/true\n", 0, 1},
		{"joined-call", "def worker ()\n\t/bin/false &\n\n(worker ())\n/bin/true\n", 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			status := runNativeCLIIn(t, t.TempDir(), []string{"do", "run", "-i"}, strings.NewReader(test.entries), &out, &errOut)
			if status != test.status || strings.Count(errOut.String(), "error[RECIPE_FAIL]") != test.reports {
				t.Fatalf("status=%d out=%q err=%q", status, out.String(), errOut.String())
			}
		})
	}
}

// Interactive cd and set change the session directory and child
// environment for later entries; default grants follow the directory.
func TestNativeInteractiveKashSessionDirectoryAndEnvironment(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries := strings.Join([]string{
		"def enter ()\n  cd sub\n",
		"enter",
		"echo written > ./note.txt",
		"pwd",
		"cd -",
		"set env/GREETING = \"hi kash\"",
		"set env/RAW = \"$KASH_TEST_NAME\"",
		"printenv GREETING RAW",
		"set env/GREETING = nil",
		"printenv GREETING ?",
		"cd ./missing",
		"set env/KEPT = \"entry-value\"",
		"set env/KEPT = true",
		"printenv KEPT",
		"def captured ()\n  set env/KEPT = \"capture-only\"\n  sh -c 'printf %s \"$KEPT\"'\n",
		"echo $(captured)",
		"printenv KEPT",
		"cd",
		"pwd",
		"cd -",
		"cd ~",
		"pwd",
		"cd ~/..",
		"pwd",
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	command := exec.Command(nativeCLIBinary(t), "do", "run", "-i")
	command.Dir, command.Env = directory, append(os.Environ(), "KASH_TEST_NAME=kash", "GREETING=inherited", "KASH_INIT=", "HOME="+filepath.Join(directory, "sub"))
	command.Stdin, command.Stdout, command.Stderr = strings.NewReader(entries), &out, &errOut
	err := command.Run()
	status := 0
	if exit, ok := err.(*exec.ExitError); ok {
		status = exit.ExitCode()
	}
	resolved, _ := filepath.EvalSymlinks(filepath.Join(directory, "sub"))
	want := resolved + "\nhi kash\n$KASH_TEST_NAME\nhi kash\nentry-value\ncapture-only\nentry-value\n" + resolved + "\n" + resolved + "\n" + filepath.Dir(resolved) + "\n"
	if status != 0 || out.String() != want {
		t.Fatalf("session state: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	if data, err := os.ReadFile(filepath.Join(directory, "sub", "note.txt")); err != nil || string(data) != "written\n" {
		t.Fatalf("redirection did not follow cd: %q %v", data, err)
	}
	if !strings.Contains(errOut.String(), "cd: not a directory") || !strings.Contains(errOut.String(), "[EXPR_INVALID]") || !strings.Contains(errOut.String(), "[CAP_DENIED]") {
		t.Fatalf("builtin errors: %q", errOut.String())
	}
}

func TestNativeKameProgramRuleDeclarations(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "program.kame"), "value = \"content\"\nrule ./out = value\ntask default : ./out\nservice dev {port=8080}\n\t/bin/echo $port\n")
	command, _, stderr := nativeCLIStart(t, directory, "program.kame")
	if status := nativeCLIWait(t, command, stderr); status != 0 {
		t.Fatalf("combined default failed with status %d: %s", status, nativeCLIRead(stderr))
	}
	if output := nativeCLIRead(filepath.Join(directory, "out")); output != "content" {
		t.Fatalf("expression body published %q", output)
	}
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "plan", "--json", "-f", "program.kame", "dev")
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), `"arguments":{"port":"8080"}`) {
		t.Fatalf("service plan missed the default argument with status %d: %s%s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--jobs=2", "--dry-run", "--allow-run", "--json", "program.kame", "dev", "port=9090")
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "9090") {
		t.Fatalf("service argument override failed with status %d: %s%s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	nativeCLIWrite(t, filepath.Join(directory, "values.km"), "answer = 42\n")
	command, _, stderr = nativeCLIStart(t, directory, "do", "run", "--jobs=2", "values.km", "answer")
	if status := nativeCLIWait(t, command, stderr); status != 2 || !strings.Contains(nativeCLIRead(stderr), "OPT_CONFLICT") {
		t.Fatalf("rule option for a values-only source returned status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeKashRuleReturnPublishesArtifactAndStopsBody(t *testing.T) {
	directory := t.TempDir()
	text := "rule ./artifact\n  if true\n    return str \"selected\" \" value\"\n  (fail! \"unreachable\")\n" +
		"task check : ./artifact\n  return\n  (fail! \"unreachable\")\n" +
		"task invalid\n  return \"no output\"\n"
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.km"), text)
	command, _, stderr := nativeCLIStart(t, directory, "check")
	if status := nativeCLIWait(t, command, stderr); status != 0 {
		t.Fatalf("return failed to stop the rule body with status %d: %s", status, nativeCLIRead(stderr))
	}
	if output := nativeCLIRead(filepath.Join(directory, "artifact")); output != "selected value" {
		t.Fatalf("typed return published %q", output)
	}
	command, _, stderr = nativeCLIStart(t, directory, "invalid")
	if status := nativeCLIWait(t, command, stderr); status != 1 || !strings.Contains(nativeCLIRead(stderr), "returning an artifact value requires one file output") {
		t.Fatalf("return without an output reported status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeKashCommandWordsComposeTypedArguments(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("KAME_TEST_WORD_SCOPE", "scope with space")
	text := "def folder = \"./two words\"\ndef flags = [\"x y\", \"z\"]\n" +
		"printf '[%s]' @build --mtime=@0 deploy@host: @(str 42) '${quoted}' " +
		"$folder/file ${env/KAME_TEST_WORD_SCOPE}/file $...flags prefix-$(printf captured)-suffix\n"
	want := "[@build][--mtime=@0][deploy@host:][@42][${quoted}]" +
		"[./two words/file][scope with space/file][x y][z][prefix-captured-suffix]"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash",
		"--allow-run", "--allow-env=KAME_TEST_WORD_SCOPE", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("command words returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeExpressionNamesResolveLexicalStandardAndEnvironment(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("KAME055_ENV_VALUE", "from environment")
	t.Setenv("kame055_env_lowercase", "unreachable")
	t.Setenv("KAME055_ENV_MISSING", "")
	if err := os.Unsetenv("KAME055_ENV_MISSING"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source, want, code string
	}{
		{source: `KAME055_ENV_VALUE`, want: `"from environment"`},
		{source: `(KAME055_ENV_VALUE => KAME055_ENV_VALUE) "lexical"`, want: `"lexical"`},
		{source: `(str => [str, std/str 42]) "lexical"`, want: `["lexical", "42"]`},
		{source: `kame055_env_lowercase`, code: "REF_MISSING"},
		{source: `KAME055_ENV_MISSING`, code: "ENV_VAR_MISSING"},
		{source: `KAME055_ENV_MISSING ?? "fallback"`, want: `"fallback"`},
		{source: `env/KAME055_ENV_MISSING`, want: `nil`},
		{source: `KAME055_ENV_DENIED ?? "fallback"`, code: "CAP_DENIED"},
	} {
		command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "expr",
			"--allow-env=KAME055_ENV_VALUE", "--allow-env=KAME055_ENV_MISSING", "--allow-env=kame055_env_lowercase", "-c", tc.source)
		status := nativeCLIWait(t, command, stderr)
		if tc.code != "" {
			if status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "["+tc.code+"]") {
				t.Errorf("%s: status %d, output %q, diagnostic %s", tc.source, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
			}
		} else if status != 0 || strings.TrimSpace(nativeCLIRead(stdout)) != tc.want {
			t.Errorf("%s: status %d, output %q, want %q, diagnostic %s", tc.source, status, nativeCLIRead(stdout), tc.want, nativeCLIRead(stderr))
		}
	}
}

func TestNativeKashSetUpdatesTheOwningBlock(t *testing.T) {
	directory := t.TempDir()
	text := `let total = 0
for increment in [1, 2, 3]
  set total = total + increment
if true
  let total = 10
  set total = total + 1
  (out! total)
with
  set total = total + 1
(out! total)
def count ()
  let count = 40
  set count = count + 2
  return count
(out! (count ()))
`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "11742" {
		t.Fatalf("set returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash",
		"-c", "let values = []", "-c", `set values = [(out! "once "), $?(printf ready).value]
(out! values.1)`)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "once ready" {
		t.Fatalf("suspended assignment returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash",
		"-c", "(out! \"forbidden\")\nlet target = 1\nset target = 2", "-c", "let closure = (ignored => target)")
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" ||
		!strings.Contains(nativeCLIRead(stderr), "[PARSE_ERR]") {
		t.Fatalf("invalid assignment returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashLetShadowsFromItsDeclaration(t *testing.T) {
	directory := t.TempDir()
	first := `let value = 1
let saved = (() => value)
def read-original ()
  return value
let earlier = value
let value = value + 1
set value = value + 1
(out! [(saved ()), (read-original ()), earlier, value])
def local ()
  let n = 10
  let previous = (() => n)
  let n = n + 10
  set n = n + 1
  return [(previous ()), n]
(out! (local ()))`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", first)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "[1,1,1,3][10,21]" {
		t.Fatalf("shadowing returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash",
		"-c", "let value = 1\nlet earlier = value", "-c", "let value = value + 1\n(out! [earlier, value])")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "[1,2]" {
		t.Fatalf("cross-fragment shadowing returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashCommandLookupPrefersLexicalBindings(t *testing.T) {
	directory := t.TempDir()
	text := `def printf value rest...
  (out! [value.count, rest])
  return "discarded"
let value = {count: 42}
let extra = [1, true]
printf $value $...extra
let callback = (x => out! x)
$callback typed
out! marker
err! warning
std/out! (str 7)
x/printf %s external`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "[42,[1,true]]typedmarker7external" || nativeCLIRead(stderr) != "warning" {
		t.Fatalf("command lookup returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	for _, tc := range []struct{ source, code string }{
		{`let printf = 42; printf (out! "forbidden")`, "EXPR_INVALID"},
		{`let printf = 42; printf (out! "forbidden") | cat`, "EXPR_INVALID"},
		{`str (out! "argument")`, "TOOL_MISSING"},
	} {
		command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", tc.source)
		if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" ||
			!strings.Contains(nativeCLIRead(stderr), "["+tc.code+"]") {
			t.Fatalf("%s: status %d, output %q, diagnostic %s", tc.source, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
	}
}

func TestNativeKashEnvironmentWritesStayInTheirExecutionUnit(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("KAME055_UNIT_VALUE", "parent")
	setup := `def update value
  set env/KAME055_UNIT_VALUE = value
  return value
update 42`
	uses := `(out! env/KAME055_UNIT_VALUE)
printf %s $(printf %s (update "capture") && sh -c 'printf %s "$KAME055_UNIT_VALUE"')
(out! KAME055_UNIT_VALUE)
with env: {KAME055_UNIT_VALUE: "stage"}
  set env/KAME055_UNIT_VALUE = "after"
  sh -c 'printf %s "$KAME055_UNIT_VALUE"'
  (out! KAME055_UNIT_VALUE)
sh -c 'printf %s "$KAME055_UNIT_VALUE"'`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash",
		"--allow-env=KAME055_UNIT_VALUE", "--allow-run", "-c", setup, "-c", uses)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "42capturecapture42stageafterafter" {
		t.Fatalf("environment unit returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", `def change ()
  defer sh -c 'printf %s "$KAME055_UNIT_VALUE"'
  set env/KAME055_UNIT_VALUE = "cleanup"
change`)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "cleanup" {
		t.Fatalf("cleanup environment returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	for _, tc := range []struct{ source, code string }{
		{`set env/KAME055_UNIT_VALUE = nil`, "CAP_DENIED"},
		{`set env/KAME055_UNIT_VALUE = true`, "EXPR_INVALID"},
		{`set env/KAME055_UNIT_VALUE = {key: "value"}`, "EXPR_INVALID"},
		{`set env/KAME055_UNIT_VALUE = "unreadable"; (out! (KAME055_UNIT_VALUE ?? "fallback"))`, "CAP_DENIED"},
	} {
		command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "--no-default-grants", "--allow-run", "-c", tc.source)
		if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" ||
			!strings.Contains(nativeCLIRead(stderr), "["+tc.code+"]") {
			t.Fatalf("%s: status %d, output %q, diagnostic %s", tc.source, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-n", "-c", `set env/KAME055_UNIT_VALUE = (out! "forbidden")`)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "" {
		t.Fatalf("dry-run environment assignment returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashRecipeEnvironmentRemovalRequiresScopedAuthority(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("KAME055_AUTH_VALUE", "original")
	file := filepath.Join(directory, "authority.km")
	nativeCLIWrite(t, file, `task default ; {authority: {read: ["KAME055_AUTH_VALUE"], unset: ["KAME055_AUTH_VALUE"]}}
  set env/KAME055_AUTH_VALUE = ""
  (out! (env/KAME055_AUTH_VALUE == ""))
  set env/KAME055_AUTH_VALUE = nil
  (out! (env/KAME055_AUTH_VALUE == nil))
  sh -c 'printf %s "${KAME055_AUTH_VALUE-unset}"'
task without-authority
  set env/KAME055_AUTH_VALUE = nil
`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-run", file)
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "truetrue") || !strings.Contains(nativeCLIRead(stdout), "unset") {
		t.Fatalf("authorized removal returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-run", file, "without-authority")
	if status := nativeCLIWait(t, command, stderr); status != 1 || !strings.Contains(nativeCLIRead(stderr)+nativeCLIRead(stdout), "[CAP_DENIED]") {
		t.Fatalf("unauthorized removal returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	for _, policy := range []string{`true`, `{read: [true]}`, `{unset: ["DUP", "DUP"]}`, `{other: []}`} {
		nativeCLIWrite(t, file, "task prepare\n  out! forbidden\ntask default : prepare ; {authority: "+policy+"}\n  out! forbidden\n")
		command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-run", file)
		if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "[EXPR_INVALID]") {
			t.Fatalf("authority %s returned status %d: %q %s", policy, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
	}
}

func TestNativeInteractiveKashEnvironmentPersistsAcrossEntries(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	input := "def change ()\n  set env/KAME055_SESSION_VALUE = \"session\"\n\nchange\n" +
		"out! (env/KAME055_SESSION_VALUE)\nsh -c 'printf %s \"$KAME055_SESSION_VALUE\"'\n"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, t.TempDir(), []string{"do", "run", "-i", "--allow-run", "--allow-env=KAME055_SESSION_VALUE"}, strings.NewReader(input), &out, &errOut)
	if status != 0 || out.String() != "sessionsession" || errOut.Len() != 0 {
		t.Fatalf("interactive environment returned status %d: %q %s", status, out.String(), errOut.String())
	}
}

func TestNativeKashFreshnessValidatesConsumedChildEnvironment(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "input"), "material")
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `rule ./out : ./input ; {authority: {read: ["OBS_REMOVED"], unset: ["OBS_REMOVED"]}}
  set env/OBS_REPLACED = "local"
  set env/OBS_REMOVED = nil
  if env/OBS_REMOVED != nil
    (fail! "removed variable")
  with env: {OBS_STAGE: "with"}
    sh -c 'printf "ran\n" >> runs; printf "%s/%s/%s/%s\n" "$OBS_PASS" "$OBS_REPLACED" "${OBS_REMOVED-unset}" "$OBS_STAGE"' > $outputs.0
    OBS_STAGE: "child" sh -c 'printf "%s/%s\n" "$OBS_PASS" "$OBS_STAGE"' >> $outputs.0
`)
	for i, inherited := range []string{"first", "first", "changed"} {
		for _, name := range []string{"OBS_REPLACED", "OBS_REMOVED", "OBS_STAGE"} {
			t.Setenv(name, fmt.Sprintf("unused-%d", i))
		}
		t.Setenv("OBS_PASS", inherited)
		command, _, stderr := nativeCLIStart(t, directory, "--allow-read", "--allow-write", "--allow-run", "./out")
		if status := nativeCLIWait(t, command, stderr); status != 0 {
			t.Fatalf("environment build %d returned status %d: %s", i, status, nativeCLIRead(stderr))
		}
		want := inherited + "/local/unset/with\n" + inherited + "/child\n"
		if got := nativeCLIRead(filepath.Join(directory, "out")); got != want {
			t.Fatalf("build %d output %q, want %q", i, got, want)
		}
		wantRuns := 1
		if i == 2 {
			wantRuns = 2
		}
		if runs := strings.Count(nativeCLIRead(filepath.Join(directory, "runs")), "ran\n"); runs != wantRuns {
			t.Fatalf("build %d executed %d times, want %d", i, runs, wantRuns)
		}
	}
}

func TestNativeKashWrittenPathSelectsExecutable(t *testing.T) {
	directory := t.TempDir()
	tool := filepath.Join(directory, "kame055-path-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf unit-path"), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "set env/PATH = " + kameString(directory) + "\nPATH: /nonexistent kame055-path-tool"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{want: "unit-path"},
		{args: []string{"--tool=kame055-path-tool=/bin/true"}},
	} {
		args := append([]string{"do", "run", "--lang", "kash"}, tc.args...)
		command, stdout, stderr := nativeCLIStart(t, directory, append(args, "-c", text)...)
		if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != tc.want {
			t.Fatalf("%v: PATH lookup returned status %d: %q %s", tc.args, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
	}
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "--allow-run="+directory, "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "[CAP_DENIED]") {
		t.Fatalf("scoped PATH lookup returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `task default ; {authority: {unset: ["PATH"]}}
  set env/PATH = nil
  kame055-path-tool
`)
	command, stdout, stderr = nativeCLIStart(t, directory, "--allow-run", "default")
	if status := nativeCLIWait(t, command, stderr); status != 1 || strings.Contains(nativeCLIRead(stdout), "unit-path") || !strings.Contains(nativeCLIRead(stderr), "[TOOL_MISSING]") {
		t.Fatalf("unset PATH lookup returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "--allow-run", "--tool=kame055-path-tool="+tool, "default")
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "unit-path") {
		t.Fatalf("tool override with unset PATH returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashDirectoryChangesStayInTheirExecutionUnit(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"first", "second", "home"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(directory, "home"))
	setup := `def move destination
  cd $destination
  return ""
move ./first
pwd
(out! (sh! "pwd").stdout)`
	uses := `printf %s $(printf %s (move ../second) && pwd)
pwd
cd -
pwd
with cwd: ./first
  cd ./second
  pwd
pwd
cd
pwd
cd ./missing || /bin/printf 'recovered\n'`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", setup, "-c", uses)
	want := strings.Join([]string{filepath.Join(directory, "first"), filepath.Join(directory, "first"), filepath.Join(directory, "second"), filepath.Join(directory, "first"), directory,
		filepath.Join(directory, "first"), filepath.Join(directory, "second"), filepath.Join(directory, "home"), "recovered", ""}, "\n")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("directory unit returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
}

func TestNativeAuthoredImportsBindOnlyExports(t *testing.T) {
	directory := t.TempDir()
	library := filepath.Join(directory, "lib")
	if err := os.Mkdir(library, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(library, "util.km"), `export answer, say
private = 40
answer = private + 2
def say () = out! "ok"
unused = fail! "an import must not evaluate definitions"
task ignored
  out! forbidden`)
	nativeCLIWrite(t, filepath.Join(library, "other.kash"), `export greet
def greet word
  out! $word`)
	kash := filepath.Join(directory, "main.kash")
	nativeCLIWrite(t, kash, `import ./lib/util
from ./lib/util.km import answer as copied
let util = "local"
out! $util ${util/answer} $copied
util/say`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-write", kash)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "local4242ok" {
		t.Fatalf("Kash import returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	kame := filepath.Join(directory, "main.km")
	nativeCLIWrite(t, kame, `from ./lib/other import greet as say
task default
  say cross-language`)
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-write", kame)
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "cross-language") {
		t.Fatalf("Kame import returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	for _, tc := range []struct{ declaration, code string }{
		{"from ./lib/util.km import private", "REF_MISSING"},
		{"import ./lib/absent", "FS_ERR"},
		{"import ./lib/ambiguous", "RES_INVALID"},
	} {
		nativeCLIWrite(t, filepath.Join(library, "ambiguous.km"), "unused = 1\n")
		nativeCLIWrite(t, filepath.Join(library, "ambiguous.kash"), "def unused = 1\n")
		nativeCLIWrite(t, kash, "out! forbidden\n"+tc.declaration+"\n")
		command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-write", kash)
		if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "["+tc.code+"]") {
			t.Fatalf("%s returned status %d: %q %s", tc.declaration, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
	}
}

func TestNativeAuthoredImportsResolveNestedModules(t *testing.T) {
	directory := t.TempDir()
	for _, folder := range []string{"lib", "lib/helpers"} {
		if err := os.Mkdir(filepath.Join(directory, folder), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(directory, "main.kash")
	facade := filepath.Join(directory, "lib", "facade.km")
	included := filepath.Join(directory, "lib", "helpers", "included.km")
	leaf := filepath.Join(directory, "lib", "leaf.kash")
	nativeCLIWrite(t, main, `import ./lib/facade
from ./lib/./facade.km import greet
facade/greet first
greet second`)
	nativeCLIWrite(t, facade, `include ./helpers/included.km
export greet`)
	nativeCLIWrite(t, included, `from ../leaf import speak
def greet = speak`)
	nativeCLIWrite(t, leaf, `export speak
def speak word
  out! $word
def unused = fail! "an import must not evaluate definitions"`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-write", main)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "firstsecond" {
		t.Fatalf("nested imports returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}

	nativeCLIWrite(t, facade, `from ./leaf import speak
export speak`)
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-write", main)
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "[DEF_INVALID]") {
		t.Fatalf("re-export returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}

	nativeCLIWrite(t, facade, `import ./leaf.kash`)
	nativeCLIWrite(t, leaf, `import ./facade.km`)
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-write", main)
	status := nativeCLIWait(t, command, stderr)
	message := nativeCLIRead(stderr)
	chain := strings.Join([]string{facade, leaf, facade}, " -> ")
	if status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(message, "[DEP_CYCLE]") || !strings.Contains(message, chain) {
		t.Fatalf("import cycle returned status %d: %q %s", status, nativeCLIRead(stdout), message)
	}
}

func TestNativeAuthoredModuleSelectsConditionalIncludes(t *testing.T) {
	directory := t.TempDir()
	main := filepath.Join(directory, "main.kash")
	module := filepath.Join(directory, "library.km")
	nativeCLIWrite(t, main, `import ./library.km
out! ${library/message}`)
	nativeCLIWrite(t, filepath.Join(directory, "configuration.km"), `mode = "release"`)
	nativeCLIWrite(t, filepath.Join(directory, "selected.km"), `message = "selected"`)
	nativeCLIWrite(t, module, `include ./configuration.km
def release () = mode == "release"
if read ./unused.km
  task ignored
    out! forbidden
if false
  include ./library.km
elif release ()
  if nil
    include ./absent.km
  else
    include? ./optional.km
    include ./selected.km
else
  include ./absent.km
export message`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-write", main)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "selected" {
		t.Fatalf("conditional includes returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}

	nativeCLIWrite(t, module, `if true
  include ./library.km`)
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-write", main)
	status := nativeCLIWait(t, command, stderr)
	message := nativeCLIRead(stderr)
	if status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(message, "[DEP_CYCLE]") ||
		!strings.Contains(message, module+" -> "+module) {
		t.Fatalf("conditional include cycle returned status %d: %q %s", status, nativeCLIRead(stdout), message)
	}
}

func TestNativeDeclarationBlocksCloseOverIterationBindings(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "input"), "input")
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.km"), `variants = ["debug", "release"]
for variant in variants
  let label = "${variant}/${env/KAME055_VARIANT_LABEL}"
  if variant == "debug"
    rule ./${variant}.txt : ./input = label
  else
    rule ./${variant}.txt : ./input
      return label
if true
  let label = "local to conditional"
  task message
    out! $label`)
	for _, label := range []string{"first", "updated"} {
		t.Setenv("KAME055_VARIANT_LABEL", label)
		command, stdout, stderr := nativeCLIStart(t, directory, "--allow-env=KAME055_VARIANT_LABEL", "--allow-read=./input", "--allow-write", "./debug.txt", "./release.txt", "message")
		if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "local to conditional") {
			t.Fatalf("declaration build returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
		for _, variant := range []string{"debug", "release"} {
			if content := nativeCLIRead(filepath.Join(directory, variant+".txt")); content != variant+"/"+label {
				t.Fatalf("%s rule captured %q with label %s", variant, content, label)
			}
		}
	}
}

func TestNativeGenerateRegistersRuleValues(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "Makefile.km")
	nativeCLIWrite(t, source, `def create name =
  rule ./${name}.txt
    return name
generate (["first", "second"] |> map create)
for name in ["nested"]
  let body = "${name}/generated"
  generate (rule ./${name}.txt = body)
generate (task default
  out! generated
)`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-write", source, "./first.txt", "./second.txt", "./nested.txt", "default")
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "generated") {
		t.Fatalf("generate returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	for _, entry := range []struct{ name, content string }{{"first", "first"}, {"second", "second"}, {"nested", "nested/generated"}} {
		if got := nativeCLIRead(filepath.Join(directory, entry.name+".txt")); got != entry.content {
			t.Errorf("generated %s wrote %q, want %q", entry.name, got, entry.content)
		}
	}

	nativeCLIWrite(t, source, `def create name =
  task conflict
    out! forbidden
generate ([1, 2] |> map create)`)
	command, stdout, stderr = nativeCLIStart(t, directory, "--allow-write", "conflict")
	status := nativeCLIWait(t, command, stderr)
	message := nativeCLIRead(stderr)
	if status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(message, "[TGT_AMBIG]") ||
		!strings.Contains(message, "call create") || !strings.Contains(message, "generate registration") {
		t.Fatalf("generate collision returned status %d: %q %s", status, nativeCLIRead(stdout), message)
	}
}

func TestNativeGenerateImportsConstructorAndPrivateBindings(t *testing.T) {
	directory := t.TempDir()
	library := filepath.Join(directory, "lib")
	if err := os.Mkdir(library, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(library, "factory.km"), `from ./labels.km import label
export create
stem = "result-"
def create name =
  rule ./${stem}${name}.txt = str label ":" name ":" (basename module/file)
`)
	nativeCLIWrite(t, filepath.Join(library, "labels.km"), "export label\nlabel = \"private\"\n")
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `import ./unused
from ./lib/factory.km import create
generate (create "item")
`)
	command, _, stderr := nativeCLIStart(t, directory, "./result-item.txt")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(filepath.Join(directory, "result-item.txt")) != "private:item:factory.km" {
		t.Fatalf("imported constructor returned status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeDeclarationIncludesSpliceIntoEachIteration(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `for variant in ["debug", "release"]
  if variant == "unused"
    include ./Kamefile
  include ./rules/variant.km
  rule ./${variant}-after.txt = final
`)
	nativeCLIWrite(t, filepath.Join(directory, "rules", "variant.km"), `label = str prefix ":" (basename module/file)
prefix = str variant "-selected"
include ./nested.km
final = str label nested-label
`)
	nativeCLIWrite(t, filepath.Join(directory, "rules", "nested.km"), `nested-label = str ":" (basename module/file)
rule ./${variant}.txt = str label nested-label
`)
	command, _, stderr := nativeCLIStart(t, directory, "./debug.txt", "./debug-after.txt", "./release.txt", "./release-after.txt")
	if status := nativeCLIWait(t, command, stderr); status != 0 {
		t.Fatalf("declaration includes returned status %d: %s", status, nativeCLIRead(stderr))
	}
	for _, variant := range []string{"debug", "release"} {
		for name, want := range map[string]string{
			variant + ".txt":       variant + "-selected:variant.km:nested.km",
			variant + "-after.txt": variant + "-selected:variant.km:nested.km",
		} {
			if got := nativeCLIRead(filepath.Join(directory, name)); got != want {
				t.Errorf("%s contains %q, want %q", name, got, want)
			}
		}
	}
}

func TestNativeRuleBodyBindsCapturesAndChangedInputs(t *testing.T) {
	directory := t.TempDir()
	target := "./out/release/common/example.txt"
	if err := os.MkdirAll(filepath.Join(directory, "out", "release", "common"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "order"} {
		nativeCLIWrite(t, filepath.Join(directory, name), name)
	}
	nativeCLIWrite(t, filepath.Join(directory, "bindings.kash"), `export snapshot
def snapshot () = [captures, changed-inputs]`)
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.km"), `import ./bindings.kash
rule ./out/{kind}/{*}/{name}.txt : ./a ./b ./a | ./order
  let job = $&(out! "$kind/$name" (json (bindings/snapshot ())))
  let done = $?(wait $job)
  return done.value`)
	for i, want := range []string{`release/example[["release","common","example"],["./a","./b"]]`, `release/example[["release","common","example"],["./b"]]`, `release/example[["release","common","example"],[]]`} {
		args := []string{"--allow-read", "--allow-write", target}
		if i == 1 {
			nativeCLIWrite(t, filepath.Join(directory, "b"), "new bytes")
			nativeCLIWrite(t, filepath.Join(directory, "order"), "changed order-only input")
		} else if i == 2 {
			args = append([]string{"--force"}, args...)
		}
		command, stdout, stderr := nativeCLIStart(t, directory, args...)
		if status := nativeCLIWait(t, command, stderr); status != 0 {
			t.Fatalf("build %d returned status %d: %q %s", i, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
		if got := nativeCLIRead(filepath.Join(directory, target)); got != want {
			t.Fatalf("build %d wrote %q, want %q", i, got, want)
		}
	}
}

func TestNativePlainPathsKeepTheirKindAtRuntime(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "input.txt"), "payload")
	source := filepath.Join(directory, "main.kash")
	nativeCLIWrite(t, source, `let file = ./input.txt
let joined = joinpath (dirname file) "input.txt"
out! (json [(file == joined), (file == "./input.txt"), (string? file), (string? module/file)])
out! "|" (str file) "|" (read file) "|"
printf "%s" $file`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-run", "--allow-read", source)
	want := "[true,false,false,false]|./input.txt|payload|./input.txt"
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("typed paths returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
}

func TestNativePathPatternsCaptureBindAndDiscover(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z.c", "a.c", "other.h", "left-right.c", "yes-yes.c"} {
		nativeCLIWrite(t, filepath.Join(directory, "src", name), "")
	}
	if err := os.Mkdir(filepath.Join(directory, "src", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(directory, "src", "sub", "nested.c"), "")
	source := filepath.Join(directory, "main.kash")
	nativeCLIWrite(t, source, `let matcher = ./src/{path:**}/{name}.c
let target = ./build/{path}/{name}.o
let fields = ./src/lib/math/vector.c |> capture matcher
let output = fields |> bind target
let regex = regex-match (pattern "{word:~[a-z]+}") "ok"
let found = files ./src/*.c
let grouped = files ./src/{name:~[a-z]+}-{name}.c
let absolute = files ${module/dir}/src/{name:~[a-z]+}-{name}.c
let nested = files ./src/{path:**}/{name}.c
let repeated = capture (pattern "{name:*}-{name:*}") "x-y"
out! (json fields) "|" (str output) "|" (regex-group "word" regex)
out! "|" (json [(output == ./build/lib/math/vector.o), (found == [./src/a.c, ./src/left-right.c, ./src/yes-yes.c, ./src/z.c]), (grouped == [./src/yes-yes.c]), (absolute == grouped), (nested == [./src/sub/nested.c]), (nil? repeated)])`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-read", source)
	want := `{"name":"vector","path":"lib/math"}|./build/lib/math/vector.o|ok|[true,true,true,true,true,true]`
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("path operations returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
}

func TestNativeDeclarationDiscoveryWatchRecoversFromOverlap(t *testing.T) {
	directory := t.TempDir()
	items := filepath.Join(directory, "items")
	if err := os.Mkdir(items, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(items, "yes-yes.txt"), "")
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `if true
  let discovered = files ./items/{name:~[a-z]+}-{name}.txt
  if (count discovered) > 1
    rule ./items/absent-absent.txt = "collision"
  rule ./out = str (count discovered)
`)
	command, stdout, stderr := nativeCLIStart(t, directory, "--json", "--watch", "./out")
	idleCount := func() int { return strings.Count(nativeCLIRead(stdout), `"type":"watch-idle"`) }
	nativeCLIAwait(t, stderr, func() bool { return idleCount() >= 1 })
	if got := nativeCLIRead(filepath.Join(directory, "out")); got != "1" {
		t.Fatalf("initial registration gave %q", got)
	}
	nativeCLIWrite(t, filepath.Join(items, "left-right.txt"), "")
	if err := os.Mkdir(filepath.Join(items, "dir-dir.txt"), 0o700); err != nil {
		t.Fatal(err)
	}
	matching := filepath.Join(items, "also-also.txt")
	nativeCLIWrite(t, matching, "")
	nativeCLIAwait(t, stderr, func() bool { return idleCount() >= 2 })
	log := nativeCLIRead(stdout)
	if !strings.Contains(log, `"code":"DISCOVERY_OVERLAP"`) || !strings.Contains(log, `output declared here: ./items/absent-absent.txt`) {
		t.Fatalf("failed registration missed the pattern's absent output:\n%s", log)
	}
	if err := os.Remove(matching); err != nil {
		t.Fatal(err)
	}
	nativeCLIAwait(t, stderr, func() bool { return idleCount() >= 3 })
	log = nativeCLIRead(stdout)
	if strings.Count(log, `"code":"DISCOVERY_OVERLAP"`) != 1 || nativeCLIRead(filepath.Join(directory, "out")) != "1" {
		t.Fatalf("watch did not recover from discovery overlap after the matching file was removed:\n%s", log)
	}
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if status := nativeCLIWait(t, command, stderr); status != 130 {
		t.Fatalf("watch returned status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeDeclarationDiscoveryRejectsContentReads(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "secret.txt"), "private contents")
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `payload = read ./secret.txt
if payload
  rule ./out = "unexpected"
`)
	command, stdout, stderr := nativeCLIStart(t, directory, "--json", "./out")
	status := nativeCLIWait(t, command, stderr)
	log := nativeCLIRead(stdout)
	if status != 1 || !strings.Contains(log, `"code":"PHASE_INVALID"`) || strings.Contains(log, "private contents") {
		t.Fatalf("registration returned status %d: %s%s", status, log, nativeCLIRead(stderr))
	}
}

func TestNativeDeclarationDiscoveryRejectsGeneratedOutput(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), `inputs = files ./items/*.txt
if (count inputs) == 0
  generate (rule ./items/absent.txt = "collision")
`)
	command, stdout, stderr := nativeCLIStart(t, directory, "--json", "./items/absent.txt")
	status := nativeCLIWait(t, command, stderr)
	log := nativeCLIRead(stdout)
	if status != 1 || !strings.Contains(log, `"code":"DISCOVERY_OVERLAP"`) || !strings.Contains(log, `output declared here: ./items/absent.txt`) {
		t.Fatalf("generated output registration returned status %d: %s%s", status, log, nativeCLIRead(stderr))
	}
}

func TestNativeModulePathsUseTheAuthoringFile(t *testing.T) {
	directory := t.TempDir()
	library := filepath.Join(directory, "lib")
	for _, name := range []string{library, filepath.Join(directory, "other")} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(library, "assets.kash")
	nativeCLIWrite(t, helper, `def assets () = module/dir
def assets-file () = module/file
def asset-path () = ${module/dir}/templates/page.kmt
export assets, assets-file`)
	t.Setenv("KAME055_MODULE_VALUE", "initial")
	kash := filepath.Join(directory, "main.kash")
	nativeCLIWrite(t, kash, `include ./lib/assets.kash
let env = "lexical"
set env/KAME055_MODULE_VALUE = "store"
cd ./other
printf '%s\n' $env ${env/KAME055_MODULE_VALUE} ${module/dir} ${module/file} (assets ()) (assets-file ()) (asset-path ())`)
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--allow-env=KAME055_MODULE_VALUE", "--allow-run", kash)
	want := strings.Join([]string{"lexical", "store", directory, kash, library, helper, filepath.Join(library, "templates", "page.kmt"), ""}, "\n")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("Kash module paths returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
	kame := filepath.Join(directory, "main.km")
	nativeCLIWrite(t, kame, `import ./lib/assets.kash as library
task default
  printf '%s\n' ${module/dir} ${module/file} (library/assets ()) (library/assets-file ())`)
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-run", kame)
	want = strings.Join([]string{directory, kame, library, helper, ""}, "\n")
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), want) {
		t.Fatalf("Kame module paths returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
}

func TestNativeKashOutputBuiltinsUseStageRouting(t *testing.T) {
	directory := t.TempDir()
	raw := []byte{0, 0xff, '\r', '\n'}
	if err := os.WriteFile(filepath.Join(directory, "bytes"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	text := `let raw = read ./bytes
out! $raw > ./out
err! $raw 2> ./err
let captured = $(out! "captured")
let diverted = $(err! "joined" 2>&1)
out! $captured $diverted
let job = $&(out! async)
let done = $?(wait $job)
out! $done.value
def cleanup ()
  defer out! later >> ./out
cleanup`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "--allow-read", "--allow-write", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "capturedjoinedasync" || nativeCLIRead(stderr) != "" {
		t.Fatalf("routed output returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	if got := nativeCLIRead(filepath.Join(directory, "out")); got != string(raw)+"later" {
		t.Fatalf("redirected stdout = %q", got)
	}
	if got := nativeCLIRead(filepath.Join(directory, "err")); got != string(raw) {
		t.Fatalf("redirected stderr = %q", got)
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", "out! piped | cat")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "piped" || nativeCLIRead(stderr) != "" {
		t.Fatalf("pipeline output returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashFunctionCapturesAndOutcomes(t *testing.T) {
	directory := t.TempDir()
	text := `def produce value
  defer out! ":cleanup"
  out! "prefix:"
  printf %s $value
  err! "notice;"
  return 42
let captured = $(produce input)
let outcome = $?(produce input)
let job = $&(produce input)
let done = $?(wait $job)
printf '[%s][%s][%s]' $captured $outcome.value $done.value
def rejected ()
  out! private
  (fail! "intentional")
let failed = $?(rejected)
printf '[%s:%s:%s]' $failed.state $failed.status $failed.error.code
def process-failure ()
  sh -c 'exit 7'
let process-outcome = $?(process-failure)
let accepted = $?(process-failure ?)
printf '[%s:%s][%s:%s:%s]' $process-outcome.state $process-outcome.status $accepted.state $accepted.status $accepted.ok`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	want := "[prefix:input:cleanup][42][42][failed:1:USER_ERROR][failed:7][completed:7:false]"
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want || nativeCLIRead(stderr) != "notice;notice;notice;" {
		t.Fatalf("function captures returned status %d: stdout %q, stderr %q", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "-l", "kash", "--capture-limit=32", "-c", "def flood ()\n  yes\nlet result = $(flood)\nout! $result")
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "[CAPTURE_LIMIT]") {
		t.Fatalf("capture bound returned status %d: stdout %q, stderr %q", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashCommandListOutcomesPreserveSelection(t *testing.T) {
	directory := t.TempDir()
	text := `let recovered = $?(sh -c 'printf one; exit 7' || printf two)
let stopped = $?(sh -c 'printf bad; exit 7' && printf skipped)
let accepted = $?(sh -c 'printf accepted; exit 8' ? || printf skipped)
let missing = $?(printf one && kame055-no-such-command || printf skipped)
let single = $?(kame055-no-such-command)
def produce ()
  out! internal
  return 42
let typed = $?(true && produce)
let job = $&(true && kame055-no-such-command)
let async = $?(wait $job)
printf '[%s:%s][%s:%s][%s:%s:%s][%s:%s][%s][%s][%s]' $recovered.status $recovered.value $stopped.status $stopped.value $accepted.state $accepted.status $accepted.value $missing.error.code $missing.value $single.error.code $typed.value $async.error.code`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	want := "[0:onetwo][7:bad][completed:8:accepted][TOOL_MISSING:one][TOOL_MISSING][42][TOOL_MISSING]"
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("command-list outcomes returned status %d: stdout %q, stderr %q", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "-l", "kash", "--no-default-grants", "--allow-write", "-c",
		"let result = $?(out! hidden && true || out! skipped)\nout! unexpected")
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "[CAP_DENIED]") {
		t.Fatalf("command-list denial returned status %d: stdout %q, stderr %q", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashWaitObservesOwnedAsyncOutcomes(t *testing.T) {
	directory := t.TempDir()
	setup := `let failed = $&(sh -c 'printf output; exit 7')
let accepted = $&(sh -c 'exit 9' ?)
let recovered = $&(printf before && false || printf after)
let missing = $&(kame-test-nonexistent-command)`
	inspect := `let failure = $?(wait $failed)
let again = $?(wait $failed)
let acceptedResult = $?(wait $accepted)
let recoveredResult = $?(wait $recovered)
let launchResult = $?(wait $missing)
printf '%s\n' $failure.state $failure.value $failure.status $again.status
printf '%s\n' $acceptedResult.state $acceptedResult.ok $acceptedResult.status $acceptedResult.effectiveStatus
printf '%s\n' $recoveredResult.value $launchResult.state $launchResult.error.code`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", setup, "-c", inspect)
	want := "failed\noutput\n7\n7\ncompleted\nfalse\n9\n0\nbeforeafter\nfailed\nTOOL_MISSING\n"
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("async waits returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
	command, _, stderr = nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", "let job = $&(sh -c 'exit 7')")
	if status := nativeCLIWait(t, command, stderr); status == 0 || !strings.Contains(nativeCLIRead(stderr), "RECIPE_FAIL") {
		t.Fatalf("unobserved async failure returned status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeKashKillSignalsAndCancelsOwnedWork(t *testing.T) {
	directory := t.TempDir()
	text := `let signalled = $&(sleep 20)
let unaffected = $&(printf intact)
kill -TERM $signalled
let terminated = $?(wait $signalled)
let cancelled = $&(sh -c 'echo $$ > child; exec sleep 20')
sh -c 'while [ ! -s child ]; do sleep 0.01; done'
kill $cancelled
let stopped = $?(wait $cancelled)
let kept = $?(wait $unaffected)
printf '%s\n' $terminated.signal $terminated.state $stopped.status $stopped.state $kept.value`
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	want := fmt.Sprintf("%d\nfailed\n130\ncancelled\nintact\n", syscall.SIGTERM)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != want {
		t.Fatalf("kill returned status %d: %q, want %q: %s", status, nativeCLIRead(stdout), want, nativeCLIRead(stderr))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "child"))))
	if err != nil || pid <= 0 || syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatalf("cancelled process was not reaped: PID %d, %v", pid, err)
	}
	command, _, stderr = nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", "kill 2147483646")
	if status := nativeCLIWait(t, command, stderr); status == 0 || !strings.Contains(nativeCLIRead(stderr), "EXPR_INVALID") {
		t.Fatalf("kill with a PID returned status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeKashFunctionsReturnValuesAndRunCleanup(t *testing.T) {
	directory := t.TempDir()
	text := "def describe amount\n" +
		"  defer (out! \"cleanup \")\n" +
		"  with\n    if amount > 0\n      return {value: amount + 1, ok: true}\n" +
		"  return nil\n" +
		"def falls-through ()\n  printf ' command'\n" +
		"let result = describe 41\n(out! result.value)\n" +
		"let implicit = falls-through ()\nif implicit == nil\n  (out! \" nil\")\n"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "cleanup 42 command nil" {
		t.Fatalf("function returns gave status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "parse", "--lang", "kash", "-c", "def old ()\n  return 7 ?\n")
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" ||
		!strings.Contains(nativeCLIRead(stderr), "PARSE_ERR") {
		t.Fatalf("old return syntax gave status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashStageSettingsUseReservedTypes(t *testing.T) {
	directory := t.TempDir()
	stageDirectory := filepath.Join(directory, "build", "release")
	if err := os.MkdirAll(stageDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(stageDirectory, "stage-proof"), "present")
	text := "def seconds = 10\ndef detached = false\ndef variant = \"release\"\n" +
		"timeout: 10 tty: false foreground: $detached env: {MODE: \"two words\"} sh -c 'printf \"[%s]\" \"$MODE\"'\n" +
		"timeout: $seconds printf '[done]'\n" +
		"cwd: ./build/(variant) timeout: 0x10 sh -c 'test -f ./stage-proof && printf \"[cwd]\"'\n"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "[two words][done][cwd]" {
		t.Fatalf("typed stage settings returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	for _, text := range []string{
		"timeout: (str 10) printf forbidden",
		"def timeout = \"10\"\ntimeout: $timeout printf forbidden",
		"tty: (str false) printf forbidden",
		"foreground: (str false) printf forbidden",
		"env: (str 10) printf forbidden",
	} {
		command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "--lang", "kash", "-c", text)
		if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" ||
			!strings.Contains(nativeCLIRead(stderr), "EXPR_INVALID") {
			t.Errorf("invalid %q returned status %d: %q %s", text, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
	}
}

func TestNativeKashConditionsUseValuesAndExplicitProcessOutcomes(t *testing.T) {
	directory := t.TempDir()
	text := "if 1 + 1 == 2 and false or $?(false).ok\n  (fail! \"unreachable\")\n" +
		"elif $?(printf chosen).value == \"chosen\"\n  (out! \"chosen\")\n" +
		"if $?(false ?).state == :completed and not $?(false ?).ok\n  (out! \" accepted\")\n" +
		"while $?(false).ok\n  (fail! \"unreachable\")\n"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "chosen accepted" {
		t.Fatalf("conditional execution returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", "if true && false\n  (out! 42)\n")
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" || !strings.Contains(nativeCLIRead(stderr), "PARSE_ERR") {
		t.Fatalf("command-list header returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashLoopsEvaluateValueHeaders(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "events"), "first\nsecond\n")
	text := "for item in if true then [2, 3] else []\n  (out! item)\n" +
		"for line from joinpath \".\" \"events\" :lines\n  (out! line)\n"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "23firstsecond" {
		t.Fatalf("value iteration headers returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "parse", "-l", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), "kind: kash-for") {
		t.Fatalf("human parse report returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeDiscoversKameProgramsInSpecificationOrder(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "src", "km"), 0o700); err != nil {
		t.Fatal(err)
	}
	names := []string{"Kamefile", "Makefile.km", "make.km", "src/km/main.km"}
	for _, name := range names {
		nativeCLIWrite(t, filepath.Join(directory, name), "task default\n")
	}
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.kmk"), "task default :\n")
	for _, name := range names {
		command, stdout, stderr := nativeCLIStart(t, directory, "do", "plan", "--json", "default")
		if status := nativeCLIWait(t, command, stderr); status != 0 || !strings.Contains(nativeCLIRead(stdout), `"source":"`+name+`"`) {
			t.Fatalf("expected %s with status %d: %s%s", name, status, nativeCLIRead(stdout), nativeCLIRead(stderr))
		}
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	command, _, stderr := nativeCLIStart(t, directory, "do", "plan", "default")
	if status := nativeCLIWait(t, command, stderr); status != 1 || !strings.Contains(nativeCLIRead(stderr), "BUILD_NO_SOURCE") {
		t.Fatalf("legacy filename was discovered with status %d: %s", status, nativeCLIRead(stderr))
	}
	nativeCLIWrite(t, filepath.Join(directory, "make.km"), "task default\n\t/bin/echo discovered\n")
	command, stdout, stderr := nativeCLIStart(t, directory, "default", "-c", `(out! "tail")`)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "discovered\ntail" {
		t.Fatalf("mixed discovery with inline work returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeConditionalRulesSelectOnlyActiveDeclarations(t *testing.T) {
	directory := t.TempDir()
	text := "active =\n  let choices = [1, 2, 3]\n  for choice in choices\n    if choice == 2\n      return true\n  return false\n" +
		"if false\n  include ./missing.km\n  rule ./artifact = \"inactive\"\nelif active\n  if active\n    include ./child.km\n    rule ./artifact = content\n  task default : ./artifact\nelse\n  rule ./artifact = \"else\"\n"
	nativeCLIWrite(t, filepath.Join(directory, "program.km"), text)
	nativeCLIWrite(t, filepath.Join(directory, "child.km"), "content = \"selected\"\n")
	command, _, stderr := nativeCLIStart(t, directory, "do", "run", "program.km")
	if status := nativeCLIWait(t, command, stderr); status != 0 {
		t.Fatalf("conditional declarations failed with status %d: %s", status, nativeCLIRead(stderr))
	}
	if output := nativeCLIRead(filepath.Join(directory, "artifact")); output != "selected" {
		t.Fatalf("active branch published %q", output)
	}
	nativeCLIWrite(t, filepath.Join(directory, "effect.km"), "active =\n  let chosen = true\n  /bin/touch ./unexpected\n  return chosen\nif active\n  task default\n")
	command, _, stderr = nativeCLIStart(t, directory, "do", "run", "--allow-run", "effect.km")
	if status := nativeCLIWait(t, command, stderr); status != 1 || !strings.Contains(nativeCLIRead(stderr), "PHASE_INVALID") ||
		!strings.Contains(nativeCLIRead(stderr), "effect.km:3:") {
		t.Fatalf("pure block command returned status %d: %s", status, nativeCLIRead(stderr))
	}
	if _, err := os.Stat(filepath.Join(directory, "unexpected")); !os.IsNotExist(err) {
		t.Fatalf("pure block command attempted to create a file: %v", err)
	}
	nativeCLIWrite(t, filepath.Join(directory, "mutation.km"), "active =\n  set env/MODE = \"release\"\n  return true\nif active\n  task default\n")
	command, _, stderr = nativeCLIStart(t, directory, "do", "run", "mutation.km")
	if status := nativeCLIWait(t, command, stderr); status != 1 || !strings.Contains(nativeCLIRead(stderr), "PHASE_INVALID") ||
		!strings.Contains(nativeCLIRead(stderr), "mutation.km:2:") {
		t.Fatalf("pure block environment assignment returned status %d: %s", status, nativeCLIRead(stderr))
	}
}

func TestNativeKameTopLevelExpressionsRequireInlineInput(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "invalid.kame"), "value = 42\n(out! value)\n")
	nativeCLIWrite(t, filepath.Join(directory, "root.km"), "include ./invalid.kame\n")
	nativeCLIWrite(t, filepath.Join(directory, "values.km"), "value = 42\n")
	nativeCLIWrite(t, filepath.Join(directory, "invalid-command.km"), "echo forbidden\n")
	for _, args := range [][]string{
		{"do", "run", "invalid.kame"},
		{"do", "run", "root.km"},
		{"do", "parse", "-l", "km", "invalid.kame"},
		{"do", "fmt", "invalid.kame"},
	} {
		command, stdout, stderr := nativeCLIStart(t, directory, args...)
		status := nativeCLIWait(t, command, stderr)
		if diagnostic := nativeCLIRead(stderr); status != 1 || nativeCLIRead(stdout) != "" ||
			!strings.Contains(diagnostic, "PARSE_ERR") || !strings.Contains(diagnostic, "invalid.kame:2:") {
			t.Errorf("%v rejected file with status %d: %s", args, status, diagnostic)
		}
	}
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "invalid-command.km")
	if status := nativeCLIWait(t, command, stderr); status != 1 || nativeCLIRead(stdout) != "" ||
		!strings.Contains(nativeCLIRead(stderr), "PARSE_ERR") {
		t.Fatalf("top-level command returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "run", "-f", "values.km", "-c", "(out! value)")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "42" {
		t.Fatalf("inline expression failed with status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "fmt", "--indent", "spaces", "--indent-width", "2", "-c", "value =\n\treturn 42\n")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "value =\n  return 42\n" {
		t.Fatalf("format with default language and explicit indentation failed with status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	command, stdout, stderr = nativeCLIStart(t, directory, "do", "fmt", "-c", "value = 1 + \\\n  2\ntask default\n  printf hello \\\n    world\n")
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "value = 1 + 2\ntask default\n\tprintf hello world\n" {
		t.Fatalf("format of continued lines returned status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashStatementMatchSelectsTypedAndPatternArms(t *testing.T) {
	directory := t.TempDir()
	text := "match 1 + 1\n  2 =>\n    (out! \"two\")\n  else => out! \"wrong\"\n" +
		"match \"./src/demo.c\"\n  ./src/{name:*}.c => out! name\n  else => out! \"wrong\"\n"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "twodemo" {
		t.Fatalf("statement match failed with status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeKashTransfersWithDefaultOperands(t *testing.T) {
	directory := t.TempDir()
	text := "def value ()\n  return\n  (out! \"unreachable\")\n" +
		"(out! (if value () == nil then \"nil\" else \"wrong\"))\n" +
		"exit; (out! \"unreachable\")\n"
	command, stdout, stderr := nativeCLIStart(t, directory, "do", "run", "-l", "kash", "-c", text)
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "nil" {
		t.Fatalf("operandless transfers failed with status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}

func TestNativeBlockValuedDefinitionReturnsTypedValue(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "values.km"), "value =\n  let x = 20 + 1\n  if x == 21\n    return {answer: x * 2}\n  return\n")
	command, stdout, stderr := nativeCLIStart(t, directory, "values.km", "-c", "(out! value.answer)")
	if status := nativeCLIWait(t, command, stderr); status != 0 || strings.TrimSpace(nativeCLIRead(stdout)) != "42" {
		t.Fatalf("value block failed with status %d: %q %s", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
}
