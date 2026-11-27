package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryPickItems(t *testing.T) {
	h, root := historyFixture(t)
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	h.now = func() time.Time { return now }
	h.record("echo one")
	now = now.Add(time.Minute)
	h.record("echo two\nsecond line")
	items := h.pickItems()
	if len(items) != 2 {
		t.Fatalf("wanted two picker items, got %d", len(items))
	}
	if items[0].Label != "echo two…" || items[0].Text != "echo two\nsecond line" {
		t.Fatalf("newest item: %+v", items[0])
	}
	if items[0].Detail != "  "+now.Local().Format("2006-01-02 15:04") {
		t.Fatalf("detail: %q", items[0].Detail)
	}
	if items[1].Label != "echo one" {
		t.Fatalf("older item: %+v", items[1])
	}

	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	h.command("history -g", &bytes.Buffer{}, &bytes.Buffer{})
	h.refresh(other)
	now = now.Add(time.Minute)
	h.record("echo other")
	items = h.pickItems()
	if len(items) != 3 || items[0].Label != "echo other" || !strings.Contains(items[0].Detail, other) {
		t.Fatalf("global picker items: %+v", items)
	}
	if !h.hasPickText("echo one") {
		t.Fatal("picker lost an eligible entry")
	}
	now = now.Add(h.maxAge)
	if h.hasPickText("echo one") {
		t.Fatal("picker accepted an expired entry")
	}
}

func TestHistoryPickerEncoding(t *testing.T) {
	text := "echo first\nsecond\t" + string(rune(0x1f)) + " and more"
	items := []historyPickItem{{Label: pickerText("echo first\nsecond"), Detail: pickerText("line\tone"), Text: text}}
	record := historyPickerInput(items)
	if strings.Count(record, "\x00") != 1 {
		t.Fatalf("encoded record: %q", record)
	}
	got, ok := historyPickerSelection(record + "\x00\n")
	if !ok || got != text {
		t.Fatalf("decoded selection %q, %v; want %q", got, ok, text)
	}
	if items[0].Label != "echo first second" || items[0].Detail != "line one" {
		t.Fatalf("picker display was not sanitized: %+v", items[0])
	}
	if _, ok := historyPickerSelection("no fields"); ok {
		t.Fatal("malformed selection was accepted")
	}
	if _, ok := historyPickerSelection("a\x1fnot-base64!"); ok {
		t.Fatal("invalid base64 was accepted")
	}
}

func TestHistoryPickerBinding(t *testing.T) {
	t.Setenv("INPUTRC", "/dev/null")
	t.Setenv("TERM", "dumb")
	h, _ := historyFixture(t)
	e := &editorReader{history: h}
	e.resetEditor()
	for _, keymap := range []string{"emacs", "emacs-standard", "vi-insert", "vi-command"} {
		if bind := e.shell.Config.Binds[keymap]["\x18r"]; bind.Action != "kash-pick-history" {
			t.Fatalf("%s Ctrl-X r binds %q", keymap, bind.Action)
		}
	}
	if _, ok := e.shell.Keymap.Commands()["kash-pick-history"]; !ok {
		t.Fatal("kash-pick-history command is not registered")
	}
}
