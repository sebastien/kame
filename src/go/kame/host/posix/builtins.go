package posix

import (
	"bytes"
	"io"
	"kame/host"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// A builtin runs a standard utility in-process, avoiding fork and exec for
// the commands interactive sessions and loops call most. The executable is
// still resolved and tracked exactly as for any other command; a builtin only
// replaces the launch, and only for system utilities whose output for the
// accepted arguments is the same in GNU coreutils and BSD. Anything else
// (options, escapes, other formats) declines, and the real executable runs.
type builtin func(args []string, dir string, stdout, stderr io.Writer) int

// builtinDirectories hold the platform's own utilities; same-named programs
// elsewhere on PATH are user programs and always execute.
var builtinDirectories = []string{"/bin", "/usr/bin"}

// builtinFor returns the in-process implementation of path invoked with args,
// or nil when the real executable must run.
func builtinFor(path string, args []string) builtin {
	dir, name := filepath.Split(path)
	dir = filepath.Clean(dir)
	system := false
	for _, candidate := range builtinDirectories {
		if dir == candidate {
			system = true
		}
	}
	if !system {
		return nil
	}
	switch name {
	case "true", "false":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				return nil // GNU interprets --help and --version.
			}
		}
		status := 0
		if name == "false" {
			status = 1
		}
		return func([]string, string, io.Writer, io.Writer) int { return status }
	case "echo":
		if !echoPortable(args) {
			return nil
		}
		return runEcho
	case "printf":
		if _, ok := printfRender(args); !ok {
			return nil
		}
		return runPrintf
	case "test", "[":
		operands := args
		if name == "[" {
			if len(args) == 0 || args[len(args)-1] != "]" {
				return nil
			}
			operands = args[:len(args)-1]
		}
		if _, ok := testEvaluate(operands, ""); !ok {
			return nil
		}
		return func(_ []string, dir string, _, _ io.Writer) int {
			result, ok := testEvaluate(operands, dir)
			if !ok {
				return 2
			}
			if result {
				return 0
			}
			return 1
		}
	}
	return nil
}

// echoPortable accepts operands that GNU and BSD echo print identically: a
// single leading -n, no other option-like first operand, and no backslashes
// (BSD honors a trailing \c; GNU interprets escapes only with -e).
func echoPortable(args []string) bool {
	operands := args
	if len(operands) > 0 && operands[0] == "-n" {
		operands = operands[1:]
	}
	if len(operands) > 0 && strings.HasPrefix(operands[0], "-") {
		return false
	}
	for _, arg := range args {
		if strings.IndexByte(arg, '\\') >= 0 {
			return false
		}
	}
	return true
}

func runEcho(args []string, _ string, stdout, stderr io.Writer) int {
	newline := true
	if len(args) > 0 && args[0] == "-n" {
		newline, args = false, args[1:]
	}
	text := strings.Join(args, " ")
	if newline {
		text += "\n"
	}
	return writeBuiltin("echo", text, stdout, stderr)
}

func runPrintf(args []string, _ string, stdout, stderr io.Writer) int {
	text, _ := printfRender(args)
	return writeBuiltin("printf", text, stdout, stderr)
}

func writeBuiltin(name, text string, stdout, stderr io.Writer) int {
	if _, err := io.WriteString(stdout, text); err != nil {
		io.WriteString(stderr, name+": write error\n")
		return 1
	}
	return 0
}

// printfRender formats a portable subset: %s and %d with optional "-" or "0"
// flags, width and precision, %%, and the escapes \n \t \r \\ \" in the
// format. The format is reused while operands remain, as in POSIX printf.
// Integer operands must be plain decimal (no octal, hex or character forms).
func printfRender(args []string) (string, bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", false
	}
	format, operands := args[0], args[1:]
	var out strings.Builder
	for {
		consumed := 0
		for i := 0; i < len(format); i++ {
			c := format[i]
			if c == '\\' {
				if i+1 >= len(format) {
					return "", false
				}
				i++
				switch format[i] {
				case 'n':
					out.WriteByte('\n')
				case 't':
					out.WriteByte('\t')
				case 'r':
					out.WriteByte('\r')
				case '\\':
					out.WriteByte('\\')
				case '"':
					out.WriteByte('"')
				default:
					return "", false
				}
				continue
			}
			if c != '%' {
				out.WriteByte(c)
				continue
			}
			i++
			if i < len(format) && format[i] == '%' {
				out.WriteByte('%')
				continue
			}
			left, zero := false, false
			for i < len(format) && (format[i] == '-' || format[i] == '0') {
				left, zero = left || format[i] == '-', zero || format[i] == '0'
				i++
			}
			width, ok := printfNumber(format, &i)
			if !ok {
				return "", false
			}
			precision := -1
			if i < len(format) && format[i] == '.' {
				i++
				if precision, ok = printfNumber(format, &i); !ok {
					return "", false
				}
			}
			if i >= len(format) {
				return "", false
			}
			operand, present := "", consumed < len(operands)
			if present {
				operand = operands[consumed]
			}
			consumed++
			var text string
			switch format[i] {
			case 's':
				if zero {
					return "", false
				}
				text = operand
				if precision >= 0 && precision < len(text) {
					text = text[:precision]
				}
			case 'd', 'i':
				if precision >= 0 {
					return "", false
				}
				value := int64(0)
				if present {
					digits := strings.TrimPrefix(strings.TrimPrefix(operand, "-"), "+")
					if digits == "" || (len(digits) > 1 && digits[0] == '0') || strings.Trim(digits, "0123456789") != "" {
						return "", false
					}
					parsed, err := strconv.ParseInt(operand, 10, 64)
					if err != nil {
						return "", false
					}
					value = parsed
				}
				text = strconv.FormatInt(value, 10)
				if zero && !left && len(text) < width {
					sign := ""
					if value < 0 {
						sign, text = "-", text[1:]
					}
					text = sign + strings.Repeat("0", width-len(sign)-len(text)) + text
				}
			default:
				return "", false
			}
			if pad := width - len(text); pad > 0 {
				if left {
					text += strings.Repeat(" ", pad)
				} else {
					text = strings.Repeat(" ", pad) + text
				}
			}
			out.WriteString(text)
		}
		if consumed == 0 && len(operands) > 0 {
			return "", false // GNU warns about the ignored operands.
		}
		operands = operands[min(consumed, len(operands)):]
		if consumed == 0 || len(operands) == 0 {
			return out.String(), true
		}
	}
}

func printfNumber(format string, i *int) (int, bool) {
	start := *i
	for *i < len(format) && format[*i] >= '0' && format[*i] <= '9' {
		*i++
	}
	if *i == start {
		return 0, true
	}
	n, err := strconv.Atoi(format[start:*i])
	return n, err == nil && n < 1<<16
}

// testEvaluate implements the POSIX test grammar for up to three operands
// with file, string and decimal integer primaries. ok is false for anything
// else, including operands that the real utility would reject; with dir ""
// it only validates the expression without touching the filesystem.
func testEvaluate(args []string, dir string) (bool, bool) {
	switch len(args) {
	case 0:
		return false, true
	case 1:
		return args[0] != "", true
	case 2:
		if args[0] == "!" {
			result, ok := testEvaluate(args[1:], dir)
			return !result, ok
		}
		return testUnary(args[0], args[1], dir)
	case 3:
		if result, ok, binary := testBinary(args[0], args[1], args[2]); binary {
			return result, ok
		}
		if args[0] == "!" {
			result, ok := testEvaluate(args[1:], dir)
			return !result, ok
		}
	}
	return false, false
}

func testUnary(op, operand, dir string) (bool, bool) {
	switch op {
	case "-n":
		return operand != "", true
	case "-z":
		return operand == "", true
	case "-e", "-f", "-d", "-s", "-L", "-h", "-p", "-S", "-b", "-c", "-r", "-w", "-x":
	default:
		return false, false
	}
	if dir == "" {
		return false, true
	}
	name := operand
	if !filepath.IsAbs(name) {
		name = filepath.Join(dir, name)
	}
	if operand == "" {
		return false, true
	}
	switch op {
	case "-r", "-w", "-x":
		mode := uint32(unix.R_OK)
		if op == "-w" {
			mode = unix.W_OK
		} else if op == "-x" {
			mode = unix.X_OK
		}
		return unix.Access(name, mode) == nil, true
	case "-L", "-h":
		info, err := os.Lstat(name)
		return err == nil && info.Mode()&os.ModeSymlink != 0, true
	}
	info, err := os.Stat(name)
	if err != nil {
		return false, true
	}
	mode := info.Mode()
	switch op {
	case "-f":
		return mode.IsRegular(), true
	case "-d":
		return mode.IsDir(), true
	case "-s":
		return info.Size() > 0, true
	case "-p":
		return mode&os.ModeNamedPipe != 0, true
	case "-S":
		return mode&os.ModeSocket != 0, true
	case "-b":
		return mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0, true
	case "-c":
		return mode&os.ModeCharDevice != 0, true
	}
	return true, true // -e
}

// testBinary reports binary=false when op is not a supported binary primary.
func testBinary(left, op, right string) (result, ok, binary bool) {
	switch op {
	case "=":
		return left == right, true, true
	case "!=":
		return left != right, true, true
	case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		a, okA := testInteger(left)
		b, okB := testInteger(right)
		if !okA || !okB {
			return false, false, true
		}
		switch op {
		case "-eq":
			return a == b, true, true
		case "-ne":
			return a != b, true, true
		case "-lt":
			return a < b, true, true
		case "-le":
			return a <= b, true, true
		case "-gt":
			return a > b, true, true
		}
		return a >= b, true, true
	}
	return false, false, false
}

// testInteger accepts plain optionally signed decimal integers; GNU and BSD
// differ on surrounding blanks and overflow, so those decline.
func testInteger(text string) (int64, bool) {
	digits := strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	value, err := strconv.ParseInt(text, 10, 64)
	return value, err == nil
}

// startBuiltin completes a single-stage request in-process, reporting the
// same started, output and terminal events as an executed child. When every
// destination is a relay, a regular file or the invocation's own descriptor,
// it completes synchronously, queueing its events directly; otherwise (a FIFO
// or device that may block) it runs on its own goroutine like a child.
func (h *Host) startBuiltin(r host.ProcessRequest, run *running, cmd *exec.Cmd, stage host.ProcessStage, fn builtin, closeAll func(), out, errout *streamWriter) {
	h.pending = append(h.pending, host.ProcessEvent{Kind: host.ProcessStarted, ID: r.ID})
	stdout, syncOut := h.builtinWriter(cmd.Stdout)
	stderr, syncErr := h.builtinWriter(cmd.Stderr)
	complete := func(queue func(host.ProcessEvent)) {
		defer close(run.done)
		status := fn(cmd.Args[1:], cmd.Dir, stdout, stderr)
		closeAll()
		effective := status
		if status > 0 && status < 256 && stage.Accepted[status] {
			effective = 0
		}
		terminal := host.ProcessEvent{Kind: host.ProcessTerminal, ID: r.ID, Status: status, RetainBytes: r.RetainBytes, HasEffectiveStatus: true, EffectiveStatus: effective}
		terminal.Stages = []host.ProcessStageResult{{Status: status, EffectiveStatus: effective, HasEffectiveStatus: true}}
		if len(r.Stages) == 0 && len(r.Argv) == 0 {
			terminal.Stages = nil
		}
		terminal.Outcome = host.ProcessOutcome(run.outcome.Load())
		out.mu.Lock()
		terminal.Stdout, terminal.StdoutTruncated = bytes.Clone(out.capture.Bytes()), out.truncated
		out.mu.Unlock()
		errout.mu.Lock()
		terminal.Stderr, terminal.StderrTruncated = bytes.Clone(errout.capture.Bytes()), errout.truncated
		errout.mu.Unlock()
		h.native.mu.Lock()
		delete(h.native.processes, r.ID)
		h.native.mu.Unlock()
		queue(terminal)
	}
	// Only statement graphs (which carry stages) complete synchronously.
	// Service probes and other bare argv requests keep a child's asynchronous
	// completion: their schedulers interleave probes with the service's own
	// progress, and an instant answer changes that interleaving.
	if syncOut && syncErr && len(r.Stages) != 0 {
		complete(func(e host.ProcessEvent) { h.pending = append(h.pending, e) })
		return
	}
	stdout, stderr = cmd.Stdout, cmd.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	go complete(h.emit)
}

// pendingWriter records relayed builtin output straight into the host's
// event queue: the builtin runs on the goroutine that drains that queue, so
// sending through the event channel could block it.
type pendingWriter struct {
	h *Host
	w *streamWriter
}

func (p pendingWriter) Write(data []byte) (int, error) {
	p.w.mu.Lock()
	event := p.w.record(data)
	p.w.mu.Unlock()
	p.h.pending = append(p.h.pending, event)
	return len(data), nil
}

// builtinWriter adapts a destination for synchronous use and reports whether
// writing to it cannot block indefinitely on another process.
func (h *Host) builtinWriter(w io.Writer) (io.Writer, bool) {
	switch dest := w.(type) {
	case nil:
		return io.Discard, true
	case *streamWriter:
		return pendingWriter{h: h, w: dest}, true
	case *os.File:
		if dest == os.Stdout || dest == os.Stderr {
			return dest, true
		}
		info, err := dest.Stat()
		return dest, err == nil && info.Mode().IsRegular()
	}
	return w, false
}
