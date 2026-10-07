package cli_test

import (
	"kame/cli"
	"kame/core"
	"solod.dev/so/bytes"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestHelpResultHasExplicitOwnership(t *testing.T) {
	inv := cli.Parse("@help", []string{"plan", "--json"})
	if !inv.OK || inv.Command != "" || inv.HelpResult == "" {
		t.Error("help response was stored as command source")
	}
	buffer := bytes.NewBuffer(mem.System, nil)
	cli.WriteJSON(&buffer, &inv)
	var value core.Value
	if !core.ParseJSON(mem.System, buffer.Bytes(), &value) {
		t.Error("help invocation JSON was invalid")
	} else {
		found := false
		for i := range value.Record {
			if value.Record[i].Key == "help" && value.Record[i].Value.Text == inv.HelpResult {
				found = true
			}
		}
		if !found {
			t.Error("help response missing from its dedicated field")
		}
		value.Free(mem.System)
	}
	buffer.Free()
	// Cleanup must not depend on a mutable command discriminator.
	inv.Name = "help"
	inv.Free()
	inv.Free()
}
