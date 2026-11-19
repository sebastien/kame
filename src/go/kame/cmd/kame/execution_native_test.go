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
				// Ignore TERM so the repeated signal arrives during graceful shutdown.
				// Both the shell and its background child belong to the recipe group.
				nativeCLIWrite(t, filepath.Join(directory, "Makefile.kmk"), "./artifact :\n\ttrap '' TERM; echo $$ > leader; sleep 30 & echo $! > child; touch ready; wait; printf leaked > @>\n")
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
					time.Sleep(30 * time.Millisecond)
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
	nativeCLIWrite(t, filepath.Join(directory, "script"), "r = {a: [\"x\", \"y\"], b: \"z\"}\nwith\n\techo (count r.a)\necho (count r.a) (get \"b\" r) (count (get \"a\" r))\n")
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
			nativeCLIWrite(t, filepath.Join(directory, "Makefile.kmk"), "service daemon : ; {ready: {argv: [\"test\", \"-f\", \"ready\"], interval-ms: 10, timeout-ms: 3000}, stop: {grace-ms: 3000}}\n\ttrap 'touch stopping; while [ ! -f release ]; do sleep .01; done; touch graceful; exit 0' TERM; echo $$ > service.pid; touch ready; while :; do sleep .01; done\nconsumer : daemon\n\ttouch consumed\n")
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
			root := filepath.Join(directory, "Makefile.kmk")
			missing := filepath.Join(directory, "missing.kmk")
			source := "include ./missing.kmk\nchosen :\n\tprintf %s @{MARK} > recovered\n"
			if mode == "nested" {
				nativeCLIWrite(t, filepath.Join(directory, "parent.kmk"), "include ./missing.kmk\n")
				source = strings.Replace(source, "./missing.kmk", "./parent.kmk", 1)
			}
			if mode == "reload" {
				nativeCLIWrite(t, root, "chosen :\n\tprintf initial > recovered\n")
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
				return strings.Contains(text, "FS_ERR") && strings.Contains(text, "cannot read included source: missing.kmk") && strings.Contains(text, "last cycle failure")
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
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.kmk"), "broken :\n\texit 7\nother :\n\texit 9\n")
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
		"lazy = 1",
		"lazy = 2",
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
	for _, fragment := range []string{"<kash:2>:1:1", "lazy definition already exists in this session: lazy", "<kash:9>:1:1"} {
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
		{"def broken\n\t(fail! \"old entry\")\n\n" + strings.Repeat("(nop ())\n", 32) + "(broken ())\n", "<kash:1>:2:10"},
		{"def one arg\n\treturn 0\n\n(one 1 2)\n", "<kash:2>:1:2"},
		{"(apply (x => x) [1, 2])\n", "<kash:1>:1:9"},
		{"bad = (x => x)\n(nop bad)\n", "<kash:1>:1:7"},
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
	status := runNativeCLIIn(t, sub, []string{"do", "run", "-i", "--allow-write=" + sub, "--allow-run"}, strings.NewReader("cd ..\necho no > ./denied.txt\necho continued\n"), &out, &errOut)
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
	nativeCLIWrite(t, init, "# startup\nlet who = \"init\"\n\nexport FROM_INIT=yes\n\ndef hello name\n\techo hello $name\n\nls ./missing-startup\necho after-failure\n")
	run := func(value, entries string) (int, string, string) {
		t.Helper()
		t.Setenv("KASH_INIT", value)
		var out, errOut bytes.Buffer
		status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
		return status, out.String(), errOut.String()
	}
	status, out, errOut := run(init, "echo $who\n(hello \"x\")\nprintenv FROM_INIT\nls ./missing-entry\n")
	if status != 2 || out != "after-failure\ninit\nhello x\nyes\n" {
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
		{"awaited", "let job = (run! :async true \"/bin/false\")\nlet result = (await job) ?? nil\n/bin/true\n", 0, 0},
		{"joined-call", "def worker\n\t/bin/false &\n\nlet result = (worker ()) ?? nil\n/bin/true\n", 0, 0},
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

// Interactive cd and export change the session directory and child
// environment for later entries; default grants follow the directory.
func TestNativeInteractiveKashSessionDirectoryAndEnvironment(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries := strings.Join([]string{
		"cd sub",
		"echo written > ./note.txt",
		"pwd",
		"cd -",
		"export GREETING=\"hi $KASH_TEST_NAME\" RAW='$KASH_TEST_NAME'",
		"printenv GREETING RAW",
		"unset GREETING",
		"printenv GREETING ?",
		"cd ./missing",
		"export KEPT=no 1bad=x",
		"printenv KEPT ?",
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	command := exec.Command(nativeCLIBinary(t), "do", "run", "-i")
	command.Dir, command.Env = directory, append(os.Environ(), "KASH_TEST_NAME=kash", "KASH_INIT=")
	command.Stdin, command.Stdout, command.Stderr = strings.NewReader(entries), &out, &errOut
	err := command.Run()
	status := 0
	if exit, ok := err.(*exec.ExitError); ok {
		status = exit.ExitCode()
	}
	resolved, _ := filepath.EvalSymlinks(filepath.Join(directory, "sub"))
	want := resolved + "\nhi kash\n$KASH_TEST_NAME\n"
	if status != 0 || out.String() != want {
		t.Fatalf("session state: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	if data, err := os.ReadFile(filepath.Join(directory, "sub", "note.txt")); err != nil || string(data) != "written\n" {
		t.Fatalf("redirection did not follow cd: %q %v", data, err)
	}
	if !strings.Contains(errOut.String(), "cd: not a directory") || !strings.Contains(errOut.String(), "export: expected NAME=VALUE: 1bad=x") {
		t.Fatalf("builtin errors: %q", errOut.String())
	}
}
