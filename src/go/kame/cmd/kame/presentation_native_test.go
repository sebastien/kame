package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEarlyPresentationDoesNotEmitStreamRecords(t *testing.T) {
	for _, test := range []struct {
		args  []string
		kind  string
		topic string
	}{
		{[]string{"do", "run", "--version", "--help"}, "help", "run"},
		{[]string{"do", "fmt", "--help", "--version"}, "help", "fmt"},
		{[]string{"do", "run", "-c", "--help", "--version"}, "version", ""},
		{[]string{"do", "bogus", "--help"}, "help", "do"},
	} {
		var out, errOut bytes.Buffer
		args := append([]string{"--json"}, test.args...)
		if status := runNativeCLI(t, args, strings.NewReader(""), &out, &errOut); status != 0 || errOut.Len() != 0 {
			t.Fatalf("args=%q status=%d stderr=%q", args, status, errOut.String())
		}
		var record map[string]any
		if err := json.Unmarshal(out.Bytes(), &record); err != nil || record["type"] != test.kind {
			t.Fatalf("args=%q output=%q error=%v", args, out.String(), err)
		}
		if test.kind == "help" && record["topic"] != test.topic {
			t.Fatalf("args=%q topic=%v", args, record["topic"])
		}
	}
}

func TestPresentationDefaultAndStructuredResults(t *testing.T) {
	for _, mode := range []string{"", "ansi", "text", "json"} {
		var out, errOut bytes.Buffer
		args := []string{"do", "inputs", "-c", "task default :"}
		if mode != "" {
			args = append([]string{"-o", mode}, args...)
		}
		if status := runNativeCLI(t, args, strings.NewReader(""), &out, &errOut); status != 0 || errOut.Len() != 0 {
			t.Fatalf("%s: status=%d err=%q", mode, status, errOut.String())
		}
		if mode == "json" {
			if !strings.Contains(out.String(), `"schema":2`) || !strings.Contains(out.String(), `"items":[]`) {
				t.Fatal(out.String())
			}
		} else if !strings.Contains(out.String(), "no inputs") || strings.Contains(out.String(), "\x1b[") {
			t.Fatal(out.String())
		}
	}
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"--json", "do", "render", "-c", "hello"}, strings.NewReader(""), &out, &errOut); status != 0 || errOut.Len() != 0 {
		t.Fatalf("render: %d %s", status, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatal(out.String())
	}
	var record map[string]any
	if json.Unmarshal([]byte(lines[1]), &record) != nil || record["type"] != "render-result" || record["data"] != "hello" {
		t.Fatal(out.String())
	}
	if json.Unmarshal([]byte(lines[2]), &record) != nil || record["type"] != "summary" || record["status"] != "success" {
		t.Fatal(out.String())
	}
}
