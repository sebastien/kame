//go:build linux

package posixcheck

import (
	"kame/host"
	"kame/host/posix"
	"kame/internal/testing"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// suspensionRun collects one request's events while a test steers it.
type suspensionRun struct {
	t      *testing.T
	h      *posix.Host
	events []posix.Event
	output strings.Builder
	done   *posix.Event
}

func startSuspensionRun(t *testing.T, r posix.Request) *suspensionRun {
	h := posix.New(t.Allocator())
	if !h.Start(r) {
		t.Fatal("start failed")
	}
	return &suspensionRun{t: t, h: h}
}

// pumpFor collects events for d, or until match returns true.
func (s *suspensionRun) pumpFor(d time.Duration, match func(posix.Event) bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.h.Pump(10)
		for {
			result := s.h.Next()
			if !result.OK {
				break
			}
			e := result.Event
			s.events = append(s.events, e)
			if e.Kind == posix.Stdout {
				s.output.Write(e.Data)
			}
			if e.Kind == posix.Terminal {
				s.done = &s.events[len(s.events)-1]
			}
			if match != nil && match(e) {
				return true
			}
		}
	}
	return false
}

func (s *suspensionRun) await(kind host.ProcessEventKind) posix.Event {
	var found posix.Event
	if !s.pumpFor(waitBudget, func(e posix.Event) bool { found = e; return e.Kind == kind }) {
		s.t.Fatalf("no event of kind %d; output %q", kind, s.output.String())
	}
	return found
}

func (s *suspensionRun) close() {
	s.h.Free()
	freeEvents(s.t.Allocator(), s.events)
}

// processState reads the state letter of pid from /proc.
func processState(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	text := string(data)
	if i := strings.LastIndexByte(text, ')'); i >= 0 && i+2 < len(text) {
		return text[i+2 : i+3]
	}
	return ""
}

// Suspend stops output until Resume; the request then completes with all
// its output and its ordinary status.
func TestSuspendAndResumeRequest(t *testing.T) {
	r := request(t.Allocator(), 801, "i=0; while [ $i -lt 6 ]; do echo $i; i=$((i+1)); sleep 0.05; done")
	defer freeRequest(t.Allocator(), &r)
	s := startSuspensionRun(t, r)
	defer s.close()
	s.await(posix.Stdout)
	if !s.h.Suspend(801) {
		t.Fatal("suspend refused")
	}
	time.Sleep(50 * time.Millisecond) // Let output already written drain.
	s.pumpFor(50*time.Millisecond, nil)
	before := s.output.String()
	s.pumpFor(400*time.Millisecond, nil)
	if s.output.String() != before || s.done != nil {
		t.Fatalf("suspended request kept running: %q -> %q", before, s.output.String())
	}
	if !s.h.Resume(801, false) {
		t.Fatal("resume refused")
	}
	s.await(posix.Terminal)
	if s.done.Status != 0 || s.done.Outcome != posix.Exited || s.output.String() != "0\n1\n2\n3\n4\n5\n" {
		t.Fatalf("resumed request: status=%d outcome=%d output=%q", s.done.Status, s.done.Outcome, s.output.String())
	}
	for _, e := range s.events {
		if e.Kind == host.ProcessStopped || e.Kind == host.ProcessContinued {
			t.Fatal("requested suspension reported a stop of its own")
		}
	}
}

// A stage stopped by SIGTSTP reports one stop and stops its peers; the host
// does not resume it until asked.
func TestStageStopStopsWholeRequest(t *testing.T) {
	stages := []host.ProcessStage{
		{Argv: []string{"/bin/sh", "-c", "echo $$; sleep 0.3; kill -TSTP $$; echo resumed"}},
		{Argv: []string{"/bin/sh", "-c", "read first; echo \"$first\"; echo peer $$; cat"}},
	}
	req := posix.Request{ID: 802, Stages: stages, Directory: ".", Environment: []string{"PATH=/usr/bin:/bin"}, RetainBytes: 1024}
	s := startSuspensionRun(t, req)
	defer s.close()
	stopped := s.await(host.ProcessStopped)
	if stopped.Signal != int(syscall.SIGTSTP) {
		t.Fatalf("stop signal %d", stopped.Signal)
	}
	if !strings.Contains(s.output.String(), "peer ") && !s.pumpFor(waitBudget, func(posix.Event) bool { return strings.Contains(s.output.String(), "peer ") }) {
		t.Fatalf("peer did not report: %q", s.output.String())
	}
	peer := 0
	for _, line := range strings.Split(s.output.String(), "\n") {
		if rest, ok := strings.CutPrefix(line, "peer "); ok {
			peer, _ = strconv.Atoi(rest)
		}
	}
	deadline := time.Now().Add(waitBudget)
	for processState(peer) != "T" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processState(peer) != "T" {
		t.Fatalf("peer stage %d not stopped: %q", peer, processState(peer))
	}
	s.pumpFor(300*time.Millisecond, nil)
	if s.done != nil || strings.Contains(s.output.String(), "resumed") {
		t.Fatal("host resumed a stopped request on its own")
	}
	if !s.h.Resume(802, false) {
		t.Fatal("resume refused")
	}
	s.await(host.ProcessContinued)
	s.await(posix.Terminal)
	if s.done.Status != 0 || !strings.Contains(s.output.String(), "resumed") {
		t.Fatalf("resumed graph: status=%d output=%q", s.done.Status, s.output.String())
	}
}

// A deadline does not advance while its request is suspended.
func TestTimeoutPausedWhileSuspended(t *testing.T) {
	r := request(t.Allocator(), 803, "echo started; sleep 0.2")
	defer freeRequest(t.Allocator(), &r)
	r.TimeoutMS = 400
	s := startSuspensionRun(t, r)
	defer s.close()
	s.await(posix.Stdout)
	s.h.Suspend(803)
	s.pumpFor(600*time.Millisecond, nil)
	s.h.Resume(803, false)
	s.await(posix.Terminal)
	if s.done.Outcome != posix.Exited || s.done.Status != 0 {
		t.Fatalf("suspended request timed out: outcome=%d status=%d", s.done.Outcome, s.done.Status)
	}
}

// Cancelling a suspended request terminates and reaps it within the grace.
func TestCancelSuspendedRequest(t *testing.T) {
	r := request(t.Allocator(), 804, "echo started; sleep 5")
	defer freeRequest(t.Allocator(), &r)
	s := startSuspensionRun(t, r)
	defer s.close()
	s.await(posix.Stdout)
	s.h.Suspend(804)
	started := time.Now()
	s.h.Cancel(804)
	s.await(posix.Terminal)
	if s.done.Outcome != posix.Cancelled || time.Since(started) > 2*time.Second {
		t.Fatalf("cancelled suspended request: outcome=%d after %v", s.done.Outcome, time.Since(started))
	}
}

// A signal reaches the request's groups and continues it when suspended.
func TestSignalContinuesSuspendedRequest(t *testing.T) {
	r := request(t.Allocator(), 805, "trap 'echo got-usr1; exit 3' USR1; echo started; while :; do sleep 0.01; done")
	defer freeRequest(t.Allocator(), &r)
	s := startSuspensionRun(t, r)
	defer s.close()
	s.await(posix.Stdout)
	s.h.Suspend(805)
	time.Sleep(50 * time.Millisecond)
	if !s.h.Signal(805, int(syscall.SIGUSR1)) {
		t.Fatal("signal refused")
	}
	s.await(posix.Terminal)
	if s.done.Status != 3 || !strings.Contains(s.output.String(), "got-usr1") {
		t.Fatalf("signalled request: status=%d output=%q", s.done.Status, s.output.String())
	}
}
