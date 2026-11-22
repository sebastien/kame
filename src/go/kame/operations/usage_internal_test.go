package operations

import (
	"encoding/json"
	"kame/core"
	"kame/internal/mem"
	"kame/lang/usage"
	"reflect"
	"testing"
)

// Spec records keep the field names, order and omissions of the spec's JSON
// form, so programs see the same values the JSON round trip produced.
func TestSpecRecordMatchesJSON(t *testing.T) {
	spec := usage.Spec{Name: "x", Help: "the x tool", Aliases: []string{"ex"},
		Flags: []usage.Flag{
			{Names: []string{"-f", "--format"}, Help: "format", Value: "f", Optional: true, Global: true, Choices: []string{"a", "b"}, Complete: "none"},
			{Names: []string{"-q"}},
		},
		Args:     []usage.Arg{{Name: "file", Help: "input", Optional: true, Variadic: true, Choices: []string{"c"}, Complete: "file"}, {Name: "rest"}},
		Commands: []usage.Spec{{Name: "run", Flags: []usage.Flag{{Names: []string{"--fast"}}}}, {}},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON core.Value
	if !core.ParseJSON(mem.System, data, &fromJSON) {
		t.Fatal("parse JSON")
	}
	direct := specRecord(mem.System, spec)
	if want, got := core.ValueSignature(fromJSON), core.ValueSignature(direct); !want.Equal(got) {
		t.Fatalf("record differs from the JSON form:\n%#v\n%#v", fromJSON, direct)
	}
	back, ok := valueSpec(nil, direct)
	if !ok || !reflect.DeepEqual(back, spec) {
		t.Fatalf("round trip: %v\n%#v", ok, back)
	}
	for _, bad := range []core.Value{
		{Kind: core.String, Text: "x"},
		{Kind: core.Record, Record: []core.RecordField{{Key: "name", Value: core.Value{Kind: core.Int, Int: 1}}}},
		{Kind: core.Record, Record: []core.RecordField{{Key: "flags", Value: core.Value{Kind: core.List, List: []core.Value{{Kind: core.String}}}}}},
	} {
		if _, ok := valueSpec(nil, bad); ok {
			t.Errorf("accepted %#v", bad)
		}
	}
	extra := core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "name", Value: core.NewString(mem.System, "y")}, {Key: "other", Value: core.Value{Kind: core.Int}}, {Key: "help", Value: core.Value{}}}}
	if got, ok := valueSpec(nil, extra); !ok || got.Name != "y" {
		t.Errorf("unknown and nil fields: %v %#v", ok, got)
	}
}
