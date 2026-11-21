package host

// ForeignShellCall is one foreground exchange with a session-owned shell.
// A zero Worker sources Command and creates a worker with the request's ID.
type ForeignShellCall struct {
	Worker      int64
	Adapter     string
	Executable  string
	Command     string
	Directory   string
	Environment []string
	Set         []string
	Unset       []string
	Args        []string
	TimeoutMS   int64
}

type ForeignShellReply struct {
	Before    []string
	After     []string
	Functions []string
}

// ForeignShellHost emits the regular process streams and a terminal event
// carrying a private ForeignShellReply for each complete exchange.
type ForeignShellHost interface {
	SupportsForeignShell(string) bool
	StartForeignShell(int64, ForeignShellCall) bool
	CloseForeignShell(int64)
}
