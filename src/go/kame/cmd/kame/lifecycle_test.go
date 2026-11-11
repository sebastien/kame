package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestRunLifecycle(t *testing.T) {
	cases := []struct {
		args               []string
		status             int
		output, diagnostic string
	}{
		{[]string{"do", "run", "--lang", "expr", "-c", "(out! 42)"}, 0, "42", ""},
		{[]string{"do", "run", "--lang", "expr", "-c", "missing"}, 1, "", ""},
		{[]string{"do", "fmt", "--lang", "expr", "-c", "[1   2]"}, 0, "[1 2]", ""},
		{[]string{"do", "run", "--unknown"}, 2, "", ""},
		{[]string{"do", "plan", "-c", "task build :\n\techo ignored", "build"}, 0, "", ""},
		{[]string{"do", "run", "--lang", "expr", "-c", "(finally! ([] 42) ([] (out! \"cleanup\")))"}, 0, "cleanup", ""},
	}
	baseline := runtime.NumGoroutine()
	for iteration := 0; iteration < 64; iteration++ {
		for _, c := range cases {
			var out, err bytes.Buffer
			status := Run(c.args, strings.NewReader("42\n"), &out, &err)
			if status != c.status || !strings.Contains(out.String(), c.output) || !strings.Contains(err.String(), c.diagnostic) {
				t.Fatalf("iteration %d args=%q: status=%d stdout=%q stderr=%q", iteration, c.args, status, out.String(), err.String())
			}
		}
	}
	runtime.GC()
	if count := runtime.NumGoroutine(); count > baseline+2 {
		t.Fatalf("CLI retained goroutines: before=%d after=%d", baseline, count)
	}
}
