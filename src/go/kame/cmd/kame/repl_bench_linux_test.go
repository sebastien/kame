//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const kashBenchHistoryCap = 128 << 10
const kashBenchPrompt = "kash session> "

func kashBenchFrame(frame, input string) bool {
	i := strings.LastIndex(frame, "\x1b[?25l")
	return i >= 0 && strings.HasSuffix(frame, "\x1b[?25h") &&
		strings.HasSuffix(stripControls(frame[i:]), kashBenchPrompt+input)
}

func kashBenchCommand(n int) string {
	return fmt.Sprintf(`(out! (str "bench-" "%04d" "\n"))`, n)
}

func kashBenchMarker(n int) string { return fmt.Sprintf("bench-%04d\r\n", n) }

// Paste each seed command and wait for its output and the next prompt.
// Sending ahead can race the editor's raw-to-cooked terminal transitions.
func kashBenchGrow(s *terminalSession, from, to int, timings *latencies) {
	for n := from + 1; n <= to; n++ {
		s.mu.Lock()
		s.output.Reset()
		s.mu.Unlock()
		command := kashBenchCommand(n)
		s.send("\x1b[200~" + command + "\x1b[201~")
		s.await(0, func(frame string) bool { return kashBenchFrame(frame, command) })
		before := s.size()
		marker := kashBenchMarker(n)
		start := time.Now()
		s.send("\r")
		s.await(before, func(frame string) bool {
			_, tail, found := strings.Cut(frame, marker)
			return found && kashBenchFrame(tail, "")
		})
		if timings != nil {
			*timings = append(*timings, time.Since(start))
		}
	}
}

func kashBenchHistory(t *testing.T, path string, submitted int, capped bool) (int, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, ok := bytes.CutPrefix(data, []byte(historyHeader))
	if !ok || len(body) > kashBenchHistoryCap {
		t.Fatalf("invalid history header or size: %d bytes", len(data))
	}
	kept := bytes.Count(body, []byte{'\n'})
	if (capped && (kept >= submitted || len(body) < kashBenchHistoryCap-1024)) ||
		(!capped && kept != submitted) {
		t.Fatalf("history after %d submissions: got %d entries and %d bytes (want capped=%t)", submitted, kept, len(body), capped)
	}
	n := submitted - kept
	for len(body) > 0 {
		line, rest, _ := bytes.Cut(body, []byte{'\n'})
		body = rest
		n++
		var entry historyEntry
		if err := json.Unmarshal(line, &entry); err != nil || entry.Text != kashBenchCommand(n) {
			t.Fatalf("expected history entry %d: got %q (%v)", n, entry.Text, err)
		}
	}
	return kept, len(data) - len(historyHeader)
}

func kashBenchRSS(t *testing.T, s *terminalSession) float64 {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", s.command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	for row := range strings.SplitSeq(string(data), "\n") {
		if value, ok := strings.CutPrefix(row, "Rss:"); ok {
			kib, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(value, " kB")))
			if err != nil {
				t.Fatal(err)
			}
			return float64(kib) / 1024
		}
	}
	t.Fatal("missing Rss in /proc smaps_rollup")
	return 0
}

func kashBenchType(s *terminalSession) (first, rest latencies) {
	const line = "echo benchmark-typing"
	for range 20 {
		s.mu.Lock()
		s.output.Reset()
		s.mu.Unlock()
		for i := range len(line) {
			before := s.size()
			start := time.Now()
			s.send(line[i : i+1])
			s.await(before, func(frame string) bool { return kashBenchFrame(frame, line[:i+1]) })
			elapsed := time.Since(start)
			if i == 0 {
				first = append(first, elapsed)
			} else {
				rest = append(rest, elapsed)
			}
		}
		before := s.size()
		s.send("\x15") // Ctrl-U clears the line without adding history.
		s.await(before, func(frame string) bool { return kashBenchFrame(frame, "") })
	}
	return first, rest
}

func TestKashInteractiveBenchmark(t *testing.T) {
	if os.Getenv("KAME_KASH_BENCH") != "1" {
		t.Skip("run kame bench")
	}
	home := t.TempDir()
	directory := filepath.Join(home, "session")
	history := filepath.Join(directory, ".kash-history", "history")
	s := startTerminalSession(t, nativeCLIBinary(t), directory, []string{
		"TERM=xterm-256color", "NO_COLOR=", "PATH=/usr/bin:/bin", "HOME=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(directory, "cache"),
		"KASH_HISTORY=" + history, "KASH_HISTORY_SCOPE=local", "KASH_HISTORY_MAX_AGE_DAYS=15",
		"KASH_HISTORY_MAX_BYTES=" + strconv.Itoa(kashBenchHistoryCap),
	}, "do", "run", "-i")
	s.await(0, func(frame string) bool { return kashBenchFrame(frame, "") })
	var submitted int
	var baseline, cappedRSS float64
	var baselineMedian, cappedMedian time.Duration
	for _, n := range []int{0, 100, 1000, 2000} {
		kashBenchGrow(s, submitted, n, nil)
		kept, size := kashBenchHistory(t, history, n, n >= 1000)
		rss := kashBenchRSS(t, s)
		first, typing := kashBenchType(s)
		after := kashBenchRSS(t, s)
		var enter latencies
		kashBenchGrow(s, n, n+20, &enter)
		median, _ := typing.percentiles()
		if n == 0 {
			baseline, baselineMedian = rss, median
		}
		if n == 1000 {
			cappedRSS, cappedMedian = rss, median
		}
		t.Logf("commands=%d history=%d entries/%d KiB rss=%.2f MiB idle, %.2f MiB after typing (idle Δ%+.2f MiB) first key: %s; typing: %s (p50 %.2fx baseline); Enter→prompt: %s",
			n, kept, size>>10, rss, after, rss-baseline, first, typing, float64(median)/float64(baselineMedian), enter)
		if n == 2000 {
			t.Logf("since history capped (1000→2000): idle RSS Δ%+.2f MiB; typing p50 %.2fx", rss-cappedRSS, float64(median)/float64(cappedMedian))
		}
		submitted = n + len(enter)
	}
	if status := s.finish(); status != 0 {
		t.Fatalf("interactive session exited %d", status)
	}
}
