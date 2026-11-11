// Command demo runs the engine examples and prints their narrative.
package main

import (
	"kame/examples/engine"
	"os"
)

func main() {
	build := examples.NewBuild(nil)
	buildReport := build.Run(os.Stdout)
	build.Free()
	if !buildReport.OK() {
		panic("engine build example failed")
	}

	stream := examples.NewStream(nil)
	streamReport := stream.Run(os.Stdout)
	stream.Free()
	if !streamReport.OK() {
		panic("engine stream example failed")
	}
}
