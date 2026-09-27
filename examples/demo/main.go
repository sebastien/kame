// Command demo runs the engine examples and prints their narrative.
package main

import (
	"kame/examples"
	"solod.dev/so/fmt"
	"solod.dev/so/mem"
)

func main() {
	build := examples.NewBuild(mem.System)
	buildReport := build.Run(fmt.Output)
	build.Free()
	if !buildReport.OK() {
		panic("engine build example failed")
	}

	stream := examples.NewStream(mem.System)
	streamReport := stream.Run(fmt.Output)
	stream.Free()
	if !streamReport.OK() {
		panic("engine stream example failed")
	}
}
