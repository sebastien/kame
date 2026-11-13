//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// Interactive Kash on a terminal lends it to foreground commands: they see a
// terminal on every standard stream and own the foreground group, captures
// keep pipes, Ctrl-C reaches only the command, and Kame regains the terminal.
func TestNativeInteractiveKashTerminalHandoff(t *testing.T) {
	binary := nativeCLIBinary(t)
	controller, device := openTestTerminal(t)
	defer controller.Close()
	command := exec.Command(binary, "do", "run", "-i")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "KASH_HISTORY="+command.Dir+"/history", "TERM=dumb")
	command.Stdin, command.Stdout, command.Stderr = device, device, device
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	device.Close()
	output := &bytes.Buffer{}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := controller.Read(buffer)
			output.Write(buffer[:n])
			if err != nil {
				return
			}
		}
	}()
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(output.String(), text) {
			if time.Now().After(deadline) {
				command.Process.Kill()
				t.Fatalf("terminal output lacks %q: %q", text, output.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	send := func(text string) {
		t.Helper()
		if _, err := controller.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("kash ")
	send("sh -c '[ -t 0 ] && [ -t 1 ] && [ -t 2 ] && echo all-terminal-$((40+2))'\r")
	waitFor("all-terminal-42")
	send("sh -c 'ps -o stat= -p $$ | grep -q + && echo foreground-$((40+2))'\r")
	waitFor("foreground-42")
	send("x = $(sh -c '[ -t 1 ] && echo terminal || echo pi''pe')\r")
	send("echo captured-$x\r")
	waitFor("captured-pipe")
	send("sleep 30\r")
	time.Sleep(300 * time.Millisecond)
	send("\x03")
	send("sh -c 'echo after-interrupt-$((40+2))'\r")
	waitFor("after-interrupt-42")
	send("\x04")
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("interactive session failed: %v: %q", err, output.String())
		}
	case <-time.After(10 * time.Second):
		command.Process.Kill()
		t.Fatalf("interactive session did not end: %q", output.String())
	}
	if strings.Contains(output.String(), "RECIPE_FAIL") {
		t.Fatalf("interrupted foreground command reported a failure: %q", output.String())
	}
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
