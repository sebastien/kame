package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if status := run(os.Args[1:]); status != 0 {
		os.Exit(status)
	}
}

func run(args []string) int {
	node := os.Getenv("KAME_NODE")
	if node == "" {
		node = "node"
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kame: cannot locate launcher: %v\n", err)
		return 1
	}
	script := filepath.Join(filepath.Dir(executable), "kame.js")
	return runNode(node, script, args, os.Stdin, os.Stdout, os.Stderr)
}

func runNode(node, script string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	executable, err := exec.LookPath(node)
	if err != nil {
		fmt.Fprintf(stderr, "kame: cannot find Node runtime %q: %v\n", node, err)
		return 1
	}
	argv := make([]string, 1, len(args)+1)
	argv[0] = script
	argv = append(argv, args...)
	command := exec.Command(executable, argv...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			if status := exit.ExitCode(); status >= 0 {
				return status
			}
		}
		fmt.Fprintf(stderr, "kame: cannot run Node runtime: %v\n", err)
		return 1
	}
	return 0
}
