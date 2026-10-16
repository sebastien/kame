package posix

import (
	"solod.dev/so/c"
	"solod.dev/so/errors"
)

var ErrOutputTimeout = errors.New("cleanup output deadline exhausted")
var ErrOutputWrite = errors.New("cannot write public output")

//so:extern nodecay
func km_cli_write_output(fd c.Int, data []byte, timeoutNS int64, written *int) c.Int {
	_, _, _ = fd, data, timeoutNS
	*written = 0
	return -1
}

// WriteOutput writes directly to the public descriptor, without a stdio buffer
// whose later flush could escape the deadline. Zero timeout means ordinary I/O;
// a negative timeout is already expired. Descriptor flags are restored on exit.
func WriteOutput(fd int, data []byte, timeoutNS int64) (int, error) {
	written := 0
	status := km_cli_write_output(c.Int(fd), data, timeoutNS, &written)
	if status == 1 { return written, ErrOutputTimeout }
	if status != 0 { return written, ErrOutputWrite }
	return written, nil
}
