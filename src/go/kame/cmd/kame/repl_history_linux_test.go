//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeInteractiveHistoryScopesAndPrivacy(t *testing.T) {
	binary := nativeCLIBinary(t)
	h, root := historyFixture(t)
	h.now = time.Now
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h.refresh(a)
	h.record("echo scope-local")
	h.refresh(b)
	h.record("echo scope-other")
	s := startTerminalSession(t, binary, a, []string{"KASH_HISTORY=" + h.path}, "do", "run", "-i")
	s.waitReading()
	s.send("\x1b[A")
	if frame := s.redraw(); !strings.Contains(frame, "scope-local") || strings.Contains(frame, "scope-other") {
		t.Fatal("terminal local recall crossed scopes")
	}
	s.send("\x18h")
	if frame := s.redraw(); !strings.Contains(frame, "scope-local") {
		t.Fatal("toggle erased the input buffer")
	}
	s.send("\x01\x0b\x1b[A")
	if frame := s.redraw(); !strings.Contains(frame, "scope-other") {
		t.Fatal("terminal global recall did not include the other scope")
	}
	s.send("\x03")
	s.waitFor("^C")
	s.send("TOKEN=terminal-secret\r")
	s.waitFor("TOKEN=terminal-secret")
	s.waitReading()
	s.send(" echo private-leading-space\r")
	s.waitFor("private-leading-space")
	s.waitReading()
	s.send("printf '%s%s\\n' history- barrier\r")
	s.waitFor("history-barrier")
	s.waitReading()
	data, err := os.ReadFile(h.path)
	if err != nil || strings.Contains(string(data), "terminal-secret") || strings.Contains(string(data), "private-leading-space") {
		t.Fatal("terminal submission persisted a rejected entry")
	}
	s.send("history clear global\r")
	s.waitFor("history clear global")
	s.waitReading()
	s.send("\x1b[A")
	frame := s.redraw()
	if strings.Contains(frame, "scope-local") || strings.Contains(frame, "scope-other") || strings.Contains(frame, "terminal-secret") {
		t.Fatal("deleted or rejected entry returned from terminal history")
	}
	s.finish()
	data, err = os.ReadFile(h.path)
	if err != nil || string(data) != historyHeader {
		t.Fatal("clear did not leave an empty versioned history store")
	}
}
