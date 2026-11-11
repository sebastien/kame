package clicheck

import (
	"bytes"
	"kame/cli"
	"kame/core"
	"kame/internal/mem"
	"kame/internal/testing"
	"strings"
)

func TestHelpDistinguishesInspectionFromMaterialization(t *testing.T) {
	topics := []string{"cat", "list", "plan", "inputs", "outputs", "span", "tools"}
	for i := range topics {
		text := cli.HelpText(topics[i])
		if topics[i] == "cat" {
			if !strings.Contains(text, "builds prerequisites") || !strings.Contains(text, "Supports -j/--jobs") {
				t.Error("cat help must describe materialization and execution options")
			}
			if strings.Contains(text, "Inspection never") || strings.Contains(text, "forbids effects") || strings.Contains(text, "execution-only flags are rejected") {
				t.Error("cat help must not promise effect-free inspection")
			}
		} else if !strings.Contains(text, "Inspection never executes recipes or applies deferred writes") {
			t.Error("inspection help must retain the read-only boundary")
		}
		mem.FreeString(mem.System, text)
	}
}

func TestHelpResultHasExplicitOwnership(t *testing.T) {
	inv := cli.Parse("@help", []string{"plan", "--json"})
	if !inv.OK || inv.Command != "" || inv.HelpResult == "" {
		t.Error("help response was stored as command source")
	}
	buffer := bytes.Buffer{}
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
	buffer.Reset()
	// Cleanup must not depend on a mutable command discriminator.
	inv.Name = "help"
	inv.Free()
	inv.Free()
}
