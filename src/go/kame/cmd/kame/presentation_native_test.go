package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestDiagnosticBacktraceUsesRetainedAuthoredSource(t *testing.T) {
	directory := t.TempDir()
	text := "bad = (first 1)\n(sh! \"rm ./source.km\")\nbad\n"
	filename := filepath.Join(directory, "source.km")
	if err := os.WriteFile(filename, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"--color=never", "--diagnostic-format=plain", "do", "run", "--allow-run", "-f", "source.km"}, nil, &out, &errOut)
	if status != 1 {
		t.Fatalf("status=%d stderr=%s", status, errOut.String())
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("source was not removed: %v", err)
	}
	for _, want := range []string{"error[EXPR_INVALID]:", "source.km:1:14", "1 | bad = (first 1)", "Trace (innermost first)", "#0  operation first", "#1  definition bad"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("missing %q in retained-source diagnostic:\n%s", want, errOut.String())
		}
	}
	if strings.Count(errOut.String(), "bad = (first 1)") != 1 {
		t.Fatal("trace repeated its primary source line: " + errOut.String())
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

func TestFailurePresentationDoesNotRepeatDiagnosticDetails(t *testing.T) {
	directory := t.TempDir()
	nativeCLIWrite(t, filepath.Join(directory, "Makefile.kmk"), "root : broken\nbroken : ; [shell: kash]\n\t$(sh -c \"exit 123\")\n")
	for _, mode := range []string{"ansi", "text"} {
		for _, format := range []string{"human", "plain"} {
			var out, errOut bytes.Buffer
			status := runNativeCLIIn(t, directory, []string{"--output", mode, "--diagnostic-format", format, "--color=never", "root"}, nil, &out, &errOut)
			text := errOut.String()
			if status != 1 || out.Len() != 0 {
				t.Fatalf("%s/%s: status=%d stdout=%q stderr=%s", mode, format, status, out.String(), text)
			}
			for _, want := range []string{"error[RECIPE_FAIL]: [broken] command substitution exited unsuccessfully", "required by root -> broken", "caused by: process exited (status 123)"} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s/%s: missing %q in:\n%s", mode, format, want, text)
				}
			}
			for _, once := range []string{"RECIPE_FAIL", "command substitution exited unsuccessfully", "status 123"} {
				if strings.Count(text, once) != 1 {
					t.Fatalf("%s/%s: repeated %q in:\n%s", mode, format, once, text)
				}
			}
			if format == "human" && !strings.Contains(text, "\n     │ broken : ; [shell: kash]\n") {
				t.Fatalf("trace excerpt is not nested under its frame:\n%s", text)
			}
		}
	}
	var out, errOut bytes.Buffer
	if status := runNativeCLIIn(t, directory, []string{"--json", "root"}, nil, &out, &errOut); status != 1 || errOut.Len() != 0 {
		t.Fatalf("JSON: status=%d stderr=%s", status, errOut.String())
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var record struct {
			Type       string `json:"type"`
			Diagnostic struct {
				Message string `json:"message"`
				Cause   struct {
					Status int `json:"status"`
				} `json:"cause"`
			} `json:"diagnostic"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Type == "diagnostic" {
			found = true
			if record.Diagnostic.Message != "command substitution exited unsuccessfully" || record.Diagnostic.Cause.Status != 123 {
				t.Fatalf("JSON lost structured process metadata: %s", line)
			}
		}
	}
	if !found {
		t.Fatal("missing JSON diagnostic: " + out.String())
	}
}
