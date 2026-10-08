package cli_test

import (
	"kame/cli"
	"solod.dev/so/bytes"
	"solod.dev/so/testing"
)

func TestResultJSONPreservesCommandEnvelopes(t *testing.T) {
	a := t.Allocator()
	buffer := bytes.NewBuffer(a, nil)
	cli.WriteInvocation(&buffer, "build", true)
	cli.WriteSummary(&buffer, "fmt", 1, true, 12, -1, 0, 0)
	cli.WriteWatchTransition(&buffer, "watch-cycle-finished", 2, "success", 24, 3, 0, 0)
	expected := "{\"schema\":1,\"type\":\"invocation-started\",\"command\":\"build\",\"dryRun\":true}\n" +
		"{\"schema\":1,\"type\":\"summary\",\"command\":\"fmt\",\"status\":\"different\",\"exitStatus\":1,\"elapsedMS\":12}\n" +
		"{\"schema\":1,\"type\":\"watch-cycle-finished\",\"cycle\":2,\"status\":\"success\",\"elapsedMS\":24,\"completed\":3,\"failed\":0,\"cancelled\":0}\n"
	if buffer.String() != expected { t.Error("command result JSON changed") }
	buffer.Free()
}

func TestDataResultJSONPreservesEmptyAndBinaryBytes(t *testing.T) {
	a := t.Allocator()
	data := []string{"", "hello", "\xff", "\xff\x00", "\xff\x00\x01"}
	encoded := []string{"", "hello", "/w==", "/wA=", "/wAB"}
	for i := range data {
		buffer := bytes.NewBuffer(a, nil)
		cli.WriteDataResult(&buffer, "artifact", "", "chosen", "", false, []byte(data[i]), true)
		encoding := "utf-8"
		if i > 1 { encoding = "base64" }
		expected := "{\"schema\":1,\"type\":\"artifact\",\"target\":\"chosen\",\"data\":\"" + encoded[i] + "\",\"encoding\":\"" + encoding + "\"}\n"
		if buffer.String() != expected { t.Error("artifact result lost byte encoding") }
		buffer.Free()
	}
}
