package main

import (
	"encoding/json"
	"fmt"
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
			nativeCLIWrite(t, filepath.Join(directory, "Makefile.kmk"), "service daemon : ; [ready: [argv: [\"test\" \"-f\" \"ready\"] interval-ms: 10 timeout-ms: 3000] stop: [grace-ms: 3000]]\n\ttrap 'touch stopping; while [ ! -f release ]; do sleep .01; done; touch graceful; exit 0' TERM; echo $$ > service.pid; touch ready; while :; do sleep .01; done\nconsumer : daemon\n\ttouch consumed\n")
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
			source := "(include ./missing.kmk)\nchosen :\n\tprintf %s @(MARK) > recovered\n"
			if mode == "nested" {
				nativeCLIWrite(t, filepath.Join(directory, "parent.kmk"), "(include ./missing.kmk)\n")
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
