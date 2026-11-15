//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openTestTerminal returns a pseudo-terminal pair: the controller the test
// drives and the terminal device given to the child.
func openTestTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminal support: ", err)
	}
	if err := unix.IoctlSetPointerInt(int(controller.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	index, err := unix.IoctlGetInt(int(controller.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	device, err := os.OpenFile("/dev/pts/"+strconv.Itoa(index), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	unix.IoctlSetWinsize(int(device.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 100})
	return controller, device
}

// terminalSession drives the CLI as a session leader on a pseudo-terminal.
type terminalSession struct {
	t       *testing.T
	command *exec.Cmd
	control *os.File
	mu      sync.Mutex
	output  bytes.Buffer
	done    chan error
}

func startTerminalSession(t *testing.T, binary, directory string, env []string, args ...string) *terminalSession {
	t.Helper()
	controller, device := openTestTerminal(t)
	command := exec.Command(binary, args...)
	command.Dir, command.Env = directory, append(append(os.Environ(), "TERM=dumb", "KASH_INIT=", "KASH_HISTORY="+directory+"/.history"), env...)
	command.Stdin, command.Stdout, command.Stderr = device, device, device
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	device.Close()
	s := &terminalSession{t: t, command: command, control: controller, done: make(chan error, 1)}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := controller.Read(buffer)
			s.mu.Lock()
			s.output.Write(buffer[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() { s.done <- command.Wait() }()
	t.Cleanup(func() {
		command.Process.Kill()
		controller.Close()
	})
	return s
}

func (s *terminalSession) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.String()
}

func (s *terminalSession) waitFor(text string) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.text(), text) {
		if time.Now().After(deadline) {
			s.command.Process.Kill()
			s.t.Fatalf("terminal output lacks %q: %q", text, s.text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *terminalSession) send(text string) {
	s.t.Helper()
	if _, err := s.control.WriteString(text); err != nil {
		s.t.Fatal(err)
	}
}

// finish ends input and returns the session exit status.
func (s *terminalSession) finish() int {
	s.t.Helper()
	s.send("\x04")
	select {
	case err := <-s.done:
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		} else if err != nil {
			s.t.Fatal(err)
		}
		return 0
	case <-time.After(10 * time.Second):
		s.command.Process.Kill()
		s.t.Fatalf("interactive session did not end: %q", s.text())
	}
	return -1
}

// Interactive Kash on a terminal lends it to foreground commands: they see a
// terminal on every standard stream and own the foreground group, captures
// keep pipes, Ctrl-C reaches only the command, typed-ahead lines run, and
// Kame regains the terminal with its modes unchanged.
func TestNativeInteractiveKashTerminalHandoff(t *testing.T) {
	s := startTerminalSession(t, nativeCLIBinary(t), t.TempDir(), nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c '[ -t 0 ] && [ -t 1 ] && [ -t 2 ] && echo all-terminal-$((40+2))'\r")
	s.waitFor("all-terminal-42")
	s.send("sh -c 'ps -o stat= -p $$ | grep -q + && echo foreground-$((40+2))'\r")
	s.waitFor("foreground-42")
	s.send("x = $(sh -c '[ -t 1 ] && echo terminal || echo pi''pe')\r")
	s.send("echo captured-$x\r")
	s.waitFor("captured-pipe")
	s.send("sleep 30\r")
	time.Sleep(300 * time.Millisecond)
	s.send("\x03")
	s.send("sh -c 'echo after-interrupt-$((40+2))'\r")
	s.waitFor("after-interrupt-42")
	s.send("sleep 1\r")
	s.send("sh -c 'echo typed-ahead-$((40+2))'\r")
	s.waitFor("typed-ahead-42")
	// Captured lines show the modes; the editor echo shows the unexpanded $(...).
	modes := regexp.MustCompile(`modes-(before|after)=([0-9a-f:]+)`)
	s.send("sh -c 'echo modes-before=$(stty -g)'\r")
	s.send("stty -icanon -echo\r")
	s.send("sh -c 'echo modes-after=$(stty -g)'\r")
	deadline := time.Now().Add(10 * time.Second)
	for len(modes.FindAllString(s.text(), -1)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if found := modes.FindAllStringSubmatch(s.text(), -1); len(found) != 2 || found[0][2] != found[1][2] {
		t.Fatalf("terminal modes changed across a command: %q", found)
	}
	if status := s.finish(); status != 0 {
		t.Fatalf("interactive session status %d: %q", status, s.text())
	}
	if strings.Contains(s.text(), "RECIPE_FAIL") {
		t.Fatalf("interrupted foreground command reported a failure: %q", s.text())
	}
}

// Ctrl-C at the prompt discards the line and sets status 130; bare kash on a
// terminal starts the REPL.
func TestNativeInteractiveKashPromptInterrupt(t *testing.T) {
	directory := t.TempDir()
	if err := os.Symlink(nativeCLIBinary(t), directory+"/kash"); err != nil {
		t.Fatal(err)
	}
	s := startTerminalSession(t, directory+"/kash", directory, nil)
	s.waitFor("kash ")
	s.send("sh -c 'echo discarded-$((40+2))'")
	time.Sleep(100 * time.Millisecond)
	s.send("\x03")
	s.waitFor("^C")
	if status := s.finish(); status != 130 {
		t.Fatalf("status after interrupt at the prompt = %d: %q", status, s.text())
	}
	if strings.Contains(s.text(), "discarded-42") {
		t.Fatalf("interrupted line ran: %q", s.text())
	}
}

// Submitted lines are appended to KASH_HISTORY and recalled by a later
// session.
func TestNativeInteractiveKashHistory(t *testing.T) {
	directory := t.TempDir()
	binary := nativeCLIBinary(t)
	s := startTerminalSession(t, binary, directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("sh -c 'echo recalled-$((40+2))'\r")
	s.waitFor("recalled-42")
	if status := s.finish(); status != 0 {
		t.Fatalf("first session status %d: %q", status, s.text())
	}
	if data, err := os.ReadFile(directory + "/.history"); err != nil || string(data) != "sh -c 'echo recalled-$((40+2))'\n" {
		t.Fatalf("history file: %q %v", data, err)
	}
	s = startTerminalSession(t, binary, directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("\x1b[A\r")
	s.waitFor("recalled-42")
	s.finish()
}

// A kash-prompt function draws the prompt from the last status; a failing or
// non-string prompt falls back to the default.
func TestNativeInteractiveKashPromptFunction(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, directory+"/init.kash", "(kash-prompt info) = (str \"P\" info.status \"-\" info.name \"> \")\n")
	s := startTerminalSession(t, nativeCLIBinary(t), directory, []string{"KASH_INIT=" + directory + "/init.kash"}, "do", "run", "-i")
	name := filepath.Base(directory)
	s.waitFor("P0-" + name + "> ")
	s.send("sh -c 'exit 3'\r")
	s.waitFor("P3-" + name + "> ")
	s.send("(kash-prompt info) = (fail! \"prompt-broke\")\r")
	s.waitFor("kash " + name + "> ")
	if strings.Count(s.text(), "prompt-broke") < 2 {
		t.Fatalf("failing prompt was not reported: %q", s.text())
	}
	s.send("(kash-prompt info) = 42\r")
	s.send("(kash-prompt info) = \"custom> \"\r")
	s.waitFor("\ncustom> ")
	if status := s.finish(); status != 0 {
		t.Fatalf("prompt session status %d: %q", status, s.text())
	}
}

// Tab completes a unique path and lists ambiguous candidates.
func TestNativeInteractiveKashCompletion(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, directory+"/uniquefile.txt", "completed-content\n")
	nativeCLIWrite(t, directory+"/lista.txt", "")
	nativeCLIWrite(t, directory+"/listb.txt", "")
	s := startTerminalSession(t, nativeCLIBinary(t), directory, nil, "do", "run", "-i")
	s.waitFor("kash ")
	s.send("cat uniq\t\r")
	s.waitFor("completed-content")
	s.send("cat list\t")
	s.waitFor("lista.txt  listb.txt")
	s.send("\x03")
	s.finish()
}

// runOnTerminal runs the CLI as a session leader on a fresh pseudo-terminal
// and returns its exit status and terminal output.
func runOnTerminal(t *testing.T, directory string, args ...string) (int, string) {
	t.Helper()
	binary := nativeCLIBinary(t)
	controller, device := openTestTerminal(t)
	defer controller.Close()
	command := exec.Command(binary, args...)
	command.Dir, command.Env = directory, append(os.Environ(), "TERM=dumb")
	command.Stdin, command.Stdout, command.Stderr = device, device, device
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	device.Close()
	output := &bytes.Buffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		for {
			n, err := controller.Read(buffer)
			output.Write(buffer[:n])
			if err != nil {
				return
			}
		}
	}()
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	var err error
	select {
	case err = <-waited:
	case <-time.After(20 * time.Second):
		command.Process.Kill()
		t.Fatalf("terminal run did not finish: %q", output.String())
	}
	<-done
	status := 0
	if exit, ok := err.(*exec.ExitError); ok {
		status = exit.ExitCode()
	}
	return status, output.String()
}

// Rule metadata lends the terminal to foreground recipe commands (051):
// all of them by default, only explicit stages once any stage selects it,
// never to captures; foreground is separate; authorization is checked.
func TestNativeRecipeTerminalHandoff(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, directory+"/Kamefile", `SHELL = kash

handoff : ; [tty: :true foreground: :true]
	sh -c '[ -t 0 ] && [ -t 1 ] && [ -t 2 ] && echo all-terminal-$((40+2))'
	sh -c 'ps -o stat= -p $$ | grep -q + && echo foreground-$((40+2))'
	x = $(sh -c '[ -t 1 ] && echo terminal || echo pi''pe')
	echo captured-$x

selective : ; [tty: :true foreground: :true]
	sh -c '[ -t 1 ] && echo first-terminal || echo first-pi''pe'
	:tty :true sh -c '[ -t 1 ] && echo second-terminal || echo second-pipe'

background : ; [tty: :true]
	sh -c '[ -t 1 ] && echo attached-$((40+2))'
	sh -c 'ps -o stat= -p $$ | grep -q + && echo fore''ground || echo background-$((40+2))'

denied :
	:tty :true sh -c 'true'

capture : ; [tty: :true]
	echo $(:tty :true sh -c 'true')

invalid : ; [foreground: :true]
	true
`)
	for _, check := range []struct{ target, want string }{
		{"handoff", "all-terminal-42"}, {"handoff", "foreground-42"}, {"handoff", "captured-pipe"},
		{"selective", "first-pipe"}, {"selective", "second-terminal"},
		{"background", "attached-42"}, {"background", "background-42"},
	} {
		status, output := runOnTerminal(t, directory, check.target)
		if status != 0 || !strings.Contains(output, check.want) {
			t.Fatalf("%s: status=%d, want %q in %q", check.target, status, check.want, output)
		}
	}
	for _, check := range []struct{ target, code string }{
		{"denied", "CAP_DENIED"}, {"capture", "EXPR_INVALID"}, {"invalid", "EXPR_INVALID"}, {"handoff", "HOST_FAIL"},
	} {
		var out, errOut bytes.Buffer
		command := exec.Command(nativeCLIBinary(t), check.target)
		command.Dir, command.Stdout, command.Stderr = directory, &out, &errOut
		// A new session has no controlling terminal: handoff cannot fall back.
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := command.Run(); err == nil || !strings.Contains(errOut.String()+out.String(), check.code) {
			t.Fatalf("%s: want %s, err=%v output=%q", check.target, check.code, err, errOut.String()+out.String())
		}
	}
}
