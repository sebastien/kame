package main

import (
	"encoding/base64"
	"kame/host/posix"
	"strings"
)

// historyPickItem is one picker row; Text is the whole entry to insert.
type historyPickItem struct {
	Label  string
	Detail string
	Text   string
}

// pickItems returns the selected view most recent first. GetLine rechecks
// expiry, and a multi-line entry shows its first line with an ellipsis.
func (h *replHistory) pickItems() []historyPickItem {
	items := make([]historyPickItem, 0, len(h.view))
	for i := len(h.view) - 1; i >= 0; i-- {
		text, _ := h.GetLine(i)
		if text == "" {
			continue
		}
		label := text
		if first, _, more := strings.Cut(text, "\n"); more {
			label = first + "…"
		}
		detail := "  " + h.view[i].Time.Local().Format("2006-01-02 15:04")
		if h.global {
			detail += "  " + h.view[i].CWD
		}
		items = append(items, historyPickItem{Label: pickerText(label), Detail: pickerText(detail), Text: text})
	}
	return items
}

// pickerText keeps display rows on one line and out of terminal control.
func pickerText(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
}

// historyPickerInput encodes records for fzf: label, detail, base64 text. The
// text is hidden and cannot corrupt the field or record separators.
func historyPickerInput(items []historyPickItem) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Label)
		b.WriteByte(0x1f)
		b.WriteString(item.Detail)
		b.WriteByte(0x1f)
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(item.Text)))
		b.WriteByte(0)
	}
	return b.String()
}

// historyPickerSelection decodes the last field of a selected record.
func historyPickerSelection(record string) (string, bool) {
	record = strings.TrimRight(record, "\x00\n")
	i := strings.LastIndexByte(record, 0x1f)
	if i < 0 {
		return "", false
	}
	text, err := base64.StdEncoding.DecodeString(record[i+1:])
	if err != nil {
		return "", false
	}
	return string(text), true
}

// hasPickText rechecks that an entry is still visible in the selected view
// when the picker returns.
func (h *replHistory) hasPickText(text string) bool {
	for i := range h.view {
		if line, _ := h.GetLine(i); line == text {
			return true
		}
	}
	return false
}

func historyPickerArgs(query, header string) []string {
	return []string{
		"--read0", "--print0",
		"--delimiter=\x1f",
		"--with-nth=1,2",
		"--nth=1",
		"--no-sort",
		"--height=40%",
		"--layout=reverse",
		"--prompt=history> ",
		"--header=" + header,
		"--query=" + pickerText(query),
	}
}

// pickHistory replaces the edited entry with the chosen one without executing
// it. shortcut: fzf/sk stand in for the 057 KASH_UI backends until they exist.
func (e *editorReader) pickHistory() {
	// The picker edits the real entry; leave completion or search state first.
	if e.shell.Keymap.Local() != "" {
		e.shell.Keymap.Commands()["abort"]()
		return
	}
	items := e.history.pickItems()
	if len(items) == 0 {
		e.shell.Hint.SetTemporary("history: no entries in this view")
		e.shell.Display.Refresh()
		return
	}
	header := "local: " + e.history.scope.Root
	if e.history.global {
		header = "global"
	}
	header = pickerText(header)
	query := string(*e.shell.Line())
	input := historyPickerInput(items)
	var selection string
	status := posix.Pick("fzf", historyPickerArgs(query, header), input, &selection)
	if status == 127 {
		status = posix.Pick("sk", historyPickerArgs(query, header), input, &selection)
	}
	switch {
	case status == 0:
		if text, ok := historyPickerSelection(selection); ok && e.history.hasPickText(text) {
			e.shell.Hint.Reset()
			// Save the edited entry before replacing it, so one undo restores it.
			e.shell.History.Save()
			e.shell.Line().Set([]rune(text)...)
			e.shell.Cursor().Set(e.shell.Line().Len())
		}
	case status == 127:
		e.shell.Hint.SetTemporary("history picker needs fzf or sk")
	case status == 1 || status == 130:
		// Dismissed or no match: keep the edited entry.
	default:
		e.shell.Hint.SetTemporary("history picker failed")
	}
	e.shell.Display.Refresh()
}
