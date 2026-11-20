//go:build linux

package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// waitCount waits until text occurs at least n times in the terminal output.
func (s *terminalSession) waitCount(text string, n int) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(s.text(), text) < n {
		if time.Now().After(deadline) {
			s.command.Process.Kill()
			s.t.Fatalf("terminal output lacks %d × %q: %q", n, text, s.text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readPID waits for a positive PID written to file.
func readPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no PID in %s", file)
	return 0
}

// readSessionPID is readPID showing the terminal on failure.
func (s *terminalSession) readPID(file string) int {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	text := s.text()
	s.t.Fatalf("no PID in %s; terminal ends %q", file, text[max(0, len(text)-1500):])
	return 0
}

// processStateOf reads the state letter of pid, empty when it is gone.
func processStateOf(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	text := string(data)
	if i := strings.LastIndexByte(text, ')'); i >= 0 && i+2 < len(text) {
		return text[i+2 : i+3]
	}
	return ""
}

func waitProcessState(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for processStateOf(pid) != want && !(want == "" && processStateOf(pid) == "Z") {
		if time.Now().After(deadline) {
			t.Fatalf("process %d state %q, want %q", pid, processStateOf(pid), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Ctrl-Z stops the foreground command and every stage of its pipeline and
// pauses the rest of the entry; jobs lists it; fg resumes it where it
// stopped and runs the remaining statement once.
func TestNativeInteractiveKashStopAndForeground(t *testing.T) {
	directory := t.TempDir()
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'echo $$ > first; sleep 1' | sh -c 'echo $$ > second; cat'; sh -c 'echo second-$((40+2))'\r")
	first, second := readPID(t, directory+"/first"), readPID(t, directory+"/second")
	s.send("\x1a")
	s.waitFor("[1]+ stopped  sh -c")
	waitProcessState(t, first, "T")
	waitProcessState(t, second, "T")
	time.Sleep(1200 * time.Millisecond)
	if strings.Contains(s.text(), "second-42") {
		t.Fatalf("the rest of a stopped entry ran: %q", s.text())
	}
	s.send("jobs\r")
	s.waitCount("[1]+ stopped  sh -c", 2)
	s.send("fg\r")
	s.waitFor("second-42")
	s.send("sh -c 'echo after-$((40+2))'\r")
	s.waitFor("after-42")
	if strings.Count(s.text(), "second-42") != 1 {
		t.Fatalf("resumed statement repeated: %q", s.text())
	}
	if status := s.finish(); status != 0 {
		t.Fatalf("session status %d: %q", status, s.text())
	}
}

// Ctrl-Z also stops an entry while Kame itself holds the terminal, here
// during a capture and an if test.
func TestNativeInteractiveKashStopDuringEvaluation(t *testing.T) {
	directory := t.TempDir()
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("let x = $(sh -c 'echo $$ > capture; sleep 0.5; echo captured'); echo got-$x\r")
	readPID(t, directory+"/capture")
	s.send("\x1a")
	s.waitFor("[1]+ stopped")
	time.Sleep(800 * time.Millisecond)
	if strings.Contains(s.text(), "got-captured") {
		t.Fatalf("stopped capture finished: %q", s.text())
	}
	s.send("fg\r")
	s.waitFor("got-captured")
	s.send("if $?(sh -c 'echo $$ > test; sleep 0.5').ok\r\tsh -c 'echo test-$((40+2))'\r\r")
	s.readPID(directory + "/test")
	s.send("\x1a")
	s.waitCount("stopped", 2)
	s.send("fg\r")
	s.waitFor("test-42")
	if status := s.finish(); status != 0 {
		t.Fatalf("session status %d: %q", status, s.text())
	}
}

// bg continues a stopped job while the prompt is shown, and its completion
// is reported before the next prompt.
func TestNativeInteractiveKashBackground(t *testing.T) {
	directory := t.TempDir()
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'echo $$ > pid; sleep 0.5'; sh -c 'echo > finished'\r")
	readPID(t, directory+"/pid")
	s.send("\x1a")
	s.waitFor("[1]+ stopped")
	s.send("bg\r")
	deadline := time.Now().Add(10 * time.Second)
	for _, err := os.Stat(directory + "/finished"); err != nil; _, err = os.Stat(directory + "/finished") {
		if time.Now().After(deadline) {
			t.Fatalf("background job did not run on: %q", s.text())
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.send("sh -c 'true'\r")
	s.waitFor("[1]  done  sh -c")
	if status := s.finish(); status != 0 {
		t.Fatalf("session status %d: %q", status, s.text())
	}
}

// A final & line starts an entry in the background; reading the terminal
// stops it, and fg gives it the terminal.
func TestNativeInteractiveKashBackgroundEntryReadsTerminal(t *testing.T) {
	s := startTerminalSession(t, nativeCLIBinary(t), t.TempDir(), nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'read line; echo read-$line'")
	time.Sleep(100 * time.Millisecond)
	s.send("\x18\r")
	time.Sleep(100 * time.Millisecond)
	s.send("&\r")
	time.Sleep(500 * time.Millisecond)
	s.send("sh -c 'true'\r")
	s.waitFor("stopped (tty input)")
	s.send("fg\r")
	time.Sleep(300 * time.Millisecond)
	s.send("typed\r")
	s.waitFor("read-typed")
	if status := s.finish(); status != 0 {
		t.Fatalf("session status %d: %q", status, s.text())
	}
}

// A one-line `cmd &` entry is a running job; kill %N cancels a job, kill
// with a signal delivers it, and x/kill runs the external
// command.
func TestNativeInteractiveKashKill(t *testing.T) {
	directory := t.TempDir()
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'echo $$ > async; sleep 30' &\r")
	async := readPID(t, directory+"/async")
	s.send("jobs\r")
	s.waitFor("[1]+ running  sh -c")
	s.send("sh -c 'trap \"echo got-usr1; exit 3\" USR1; echo $$ > trapped; while :; do sleep 0.05; done'\r")
	readPID(t, directory+"/trapped")
	s.send("\x1a")
	s.waitFor("[2]+ stopped")
	s.send("kill -USR1 %2\r")
	s.waitFor("got-usr1")
	s.send("kill %1\r")
	waitProcessState(t, async, "")
	s.send("x/kill 2147483646\r")
	s.waitFor("No such process")
	s.send("sh -c 'echo end-$((40+2))'\r")
	s.waitFor("end-42")
	if strings.Contains(s.text(), "kill: expected %N") {
		t.Fatalf("kill without a job was taken as a job command: %q", s.text())
	}
	s.finish()
}

// The first end of input with a stopped job reports it and continues; the
// second ends the session and reaps the job.
func TestNativeInteractiveKashEndWithStoppedJob(t *testing.T) {
	directory := t.TempDir()
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'echo $$ > pid; sleep 30'\r")
	pid := readPID(t, directory+"/pid")
	s.send("\x1a")
	s.waitFor("[1]+ stopped")
	s.waitReading()
	s.send("\x04")
	s.waitFor("there are stopped jobs")
	if processStateOf(pid) != "T" {
		t.Fatalf("stopped job changed at the first end: %q", processStateOf(pid))
	}
	s.finish()
	waitProcessState(t, pid, "")
}

// A stopped job leaves the session's terminal modes in place; fg restores
// the modes the job had set, and the session gets its own back afterwards.
func TestNativeInteractiveKashJobTerminalModes(t *testing.T) {
	s := startTerminalSession(t, nativeCLIBinary(t), t.TempDir(), nil, "do", "run", "-i")
	s.waitFor("kash ")
	// Output lines hold the modes; the editor's echo shows the unexpanded $(...).
	mode := func(name string) string {
		s.t.Helper()
		pattern := regexp.MustCompile(name + `-modes=([0-9a-f:]+)`)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if found := pattern.FindStringSubmatch(s.text()); found != nil {
				return found[1]
			}
			time.Sleep(20 * time.Millisecond)
		}
		s.t.Fatalf("no %s modes: %q", name, s.text())
		return ""
	}
	modes := map[string]string{}
	s.send("sh -c 'echo base-modes=$(stty -g)'\r")
	modes["base"] = mode("base")
	s.send("sh -c 'stty -echo; sleep 0.6; echo job-modes=$(stty -g)'\r")
	time.Sleep(300 * time.Millisecond)
	s.send("\x1a")
	s.waitFor("[1]+ stopped")
	s.send("sh -c 'echo stopped-modes=$(stty -g)'\r")
	modes["stopped"] = mode("stopped")
	s.send("fg\r")
	modes["job"] = mode("job")
	s.send("sh -c 'echo after-modes=$(stty -g)'\r")
	modes["after"] = mode("after")
	if modes["base"] == "" || modes["stopped"] != modes["base"] || modes["after"] != modes["base"] || modes["job"] == modes["base"] {
		t.Fatalf("terminal modes across a stopped job: %q", modes)
	}
	if status := s.finish(); status != 0 {
		t.Fatalf("session status %d: %q", status, s.text())
	}
}

// The prompt sees the number of jobs; job commands reject unknown jobs,
// missing jobs, and unknown signals without other effect.
func TestNativeInteractiveKashJobCommandsAndPrompt(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, directory+"/init.kash", "def kash-prompt info = (str \"J\" info.jobs \"> \")\n")
	s := startTerminalSession(t, nativeCLIBinary(t), directory, []string{"KASH_INIT=" + directory + "/init.kash"}, "do", "run", "-i")
	s.waitFor("J0> ")
	s.send("fg\r")
	s.waitFor("fg: no current job")
	s.send("bg %9\r")
	s.waitFor("bg: no such job: %9")
	s.send("sh -c 'echo $$ > pid; sleep 30'\r")
	pid := readPID(t, directory+"/pid")
	s.send("\x1a")
	s.waitFor("J1> ")
	s.send("kill -NOPE %1\r")
	s.waitFor("kill: unknown signal: -NOPE")
	s.send("fg 1\r")
	s.waitFor("fg: expected %N: 1")
	if processStateOf(pid) != "T" {
		t.Fatalf("a rejected job command changed the job: %q", processStateOf(pid))
	}
	s.send("kill %1\r")
	waitProcessState(t, pid, "")
	before := len(s.raw())
	s.send("sh -c 'true'\r")
	s.waitFrame(before, func(frame string) bool { return strings.Contains(frame, "J0> ") })
	s.finish()
}

// A one-line `cmd &` graph attaches the terminal in the background: reading
// it stops the job, and fg gives it the terminal. fg of a running & job waits
// for it to finish before the next entry runs.
func TestNativeInteractiveKashAsyncGraphTerminal(t *testing.T) {
	s := startTerminalSession(t, nativeCLIBinary(t), t.TempDir(), nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'read line; echo read-$line' &\r")
	time.Sleep(500 * time.Millisecond)
	s.send("sh -c 'true'\r")
	s.waitFor("stopped (tty input)")
	s.send("fg\r")
	time.Sleep(300 * time.Millisecond)
	s.send("typed\r")
	s.waitFor("read-typed")
	s.send("sh -c 'sleep 0.4; echo slept-$((40+2))' &\r")
	s.send("fg\r")
	s.send("sh -c 'echo after-$((40+2))'\r")
	s.waitFor("after-42")
	if text := s.text(); !strings.Contains(text, "slept-42") || strings.Index(text, "slept-42") > strings.Index(text, "after-42") {
		t.Fatalf("fg returned before its & graph finished: %q", text)
	}
	if status := s.finish(); status != 0 {
		t.Fatalf("session status %d: %q", status, s.text())
	}
}

func TestNativeInteractiveKashNestedAsyncAttribution(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, directory+"/init.kash", "def kash-prompt info = (str \"S\" info.status \"J\" info.jobs \"> \")\n")
	s := startTerminalSession(t, nativeCLIBinary(t), directory, []string{"KASH_INIT=" + directory + "/init.kash"}, "do", "run", "-i")
	s.waitFor("S0J0> ")
	s.send("let job = (run! :async true \"/bin/sleep\" (if (run! :async true \"/bin/false\") then \"0.1\" else \"0.1\"))\r")
	s.send("wait %+\r")
	s.waitFor("S1J0> ")
	// Only the outer handle is saved. The inner failure belongs to the same job,
	// was consumed by wait, and must not be raised again by the invocation join.
	s.send("let result = (await job)\r")
	s.waitFor("let result = (await job)\r\r\nS0J0> ")
	s.send("let owned = $&(true)\r")
	s.send("wait $owned\r")
	s.send("printf 'wait-finished-%s\\n' 42\r")
	s.waitFor("wait-finished-42\r\n")
	_, afterWait, _ := strings.Cut(s.text(), "wait $owned\r\r\n")
	beforeNext, _, _ := strings.Cut(afterWait, "printf 'wait-finished-")
	if !strings.Contains(beforeNext, "S0J0> ") {
		t.Fatalf("waiting on an owned handle returned an unexpected status: %q", beforeNext)
	}
	if status := s.finish(); status != 0 {
		t.Fatalf("nested async outcome was lost or repeated: status=%d out=%q", status, s.text())
	}
}

// wait observes a job's status; %+ and %- select the current and previous
// jobs, which jobs marks; a job cancelled by kill is listed as killed.
func TestNativeInteractiveKashWaitAndJobSelectors(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, directory+"/init.kash", "def kash-prompt info = (str \"S\" info.status \"J\" info.jobs \"> \")\n")
	s := startTerminalSession(t, nativeCLIBinary(t), directory, []string{"KASH_INIT=" + directory + "/init.kash"}, "do", "run", "-i")
	s.waitFor("S0J0> ")
	s.send("sh -c 'sleep 0.5; exit 3' &\r")
	s.waitFor("S0J1> ")
	s.send("sh -c 'echo $$ > pid; sleep 30'\r")
	pid := readPID(t, directory+"/pid")
	s.send("\x1a")
	s.waitFor("[2]+ stopped")
	s.send("jobs\r")
	s.waitFor("[1]- running  sh -c 'sleep")
	s.waitFor("[2]+ stopped  sh -c 'echo")
	s.send("wait %-\r")
	s.waitFor("S3J1> ")
	s.send("kill %-\r")
	s.waitFor("kill: no previous job")
	s.send("kill %+\r")
	waitProcessState(t, pid, "")
	s.send("jobs\r")
	s.waitFor("[2]  killed  sh -c 'echo")
	s.send("wait\r")
	// Match this entry's prompt, not the identical one from session startup.
	s.waitFor("wait\r\r\nS0J0> ")
	if strings.Contains(s.text(), "[1]  exit 3") {
		t.Fatalf("a waited job was reported again: %q", s.text())
	}
	s.finish()
}

// A session ending on --timeout cancels its background jobs instead of
// joining them.
func TestNativeInteractiveKashTimeoutCancelsJobs(t *testing.T) {
	directory := t.TempDir()
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i", "--timeout", "1500")
	s.waitFor("kash ")
	s.send("sh -c 'echo $$ > pid; sleep 30' &\r")
	pid := readPID(t, directory+"/pid")
	s.send("sleep 30\r")
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		s.command.Process.Kill()
		t.Fatalf("timed-out session joined its background job: %q", s.text())
	}
	waitProcessState(t, pid, "")
}
