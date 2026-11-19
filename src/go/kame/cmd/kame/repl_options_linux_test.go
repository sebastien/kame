//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeInteractiveOptionsHistory(t *testing.T) {
	binary := nativeCLIBinary(t)
	h, root := historyFixture(t)
	o := optionsFixture(t)
	t.Setenv("KASH_INIT", "")
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h.refresh(a)
	h.record("echo option-local")
	h.refresh(b)
	h.record("echo option-other")
	if _, status := o.command("option -g history-scope global", &bytes.Buffer{}, &bytes.Buffer{}); status != 0 {
		t.Fatal("cannot save global history scope")
	}
	s := startTerminalSession(t, binary, a, []string{"KASH_HISTORY=" + h.path}, "do", "run", "-i")
	s.waitReading()
	s.send("\x1b[A")
	if frame := s.redraw(); !strings.Contains(frame, "option-other") {
		t.Fatal("global option was not applied before initial recall")
	}
	before := len(s.raw())
	s.send("\x01\x0boption history-scope local\r")
	s.waitReadingAfterSubmission(before)
	s.send("\x1b[A")
	if frame := s.redraw(); !strings.Contains(frame, "option-local") || strings.Contains(frame, "option-other") {
		t.Fatal("local option did not change history recall")
	}
	s.send("\x01\x0bhistory scope global\roption history-scope\r")
	s.waitFor("history-scope: global (local;")
	s.waitReading()
	s.send("\x18hoption history-scope\r")
	s.waitFor("history-scope: local (local;")
	s.finish()
	data := nativeCLIRead(h.path)
	if strings.Contains(data, `"text":"option `) {
		t.Fatal("option-management entries were recorded")
	}
}
