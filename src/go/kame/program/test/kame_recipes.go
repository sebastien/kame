package programcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"os"
)

func TestKameBodyPublishesExactContentAndReusesTrackedInputs(t *testing.T) {

	dir, err := os.MkdirTemp("", "kame-value-body-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("Ada\n"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	sources := []string{
		"rule ./output : ./input = (str \"Hello, \"\n\t(strip (text (read inputs.0))) \"!\\n\")\n",
		"rule ./output : ./input = read inputs.0\n",
		"rule ./output : ./input = \"\"\n",
		"rule ./output : ./input = \"\"\"first\n  second\n\"\"\"\n",
		"rule ./output : ./input = \"first\n  second\n\"\n",
		"rule ./output : ./input\n    return \"\"\"first\n      second\n    \"\"\"\n",
		"rule ./output : ./input = \"\"\"\n@if false\ndata\n@end\n\"\"\"\n",
		"rule ./output : ./input\n\treturn (read inputs.0)\n",
	}
	wants := []string{"Hello, Ada!\n", "Ada\n", "", "first\n  second\n", "first\n  second\n", "first\n  second\n", "\n@if false\ndata\n@end\n", "Ada\n"}
	for i := range sources {
		if code, _ := materializeSource(t, dir, sources[i], true, false); code != "" {
			t.Errorf("body %d failed: %s", i, code)
			continue
		}
		if !tempMatches(t, dir+"/output", wants[i]) {
			t.Error("body content was altered")
		}
		code, fresh := materializeSource(t, dir, sources[i], true, false)
		if code != "" || !fresh {
			t.Error("unchanged Kame body did not reuse its output: " + code)
		}
	}
}

func TestComputedRootCapturePrerequisiteExecutesAndTracksChanges(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-computed-input-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	source := "ROOT = \".\"\nrule ./out{path} : ${ROOT}/input-{path} = read inputs.0\n"
	for i := 0; i < 3; i++ {
		want := "first"
		if i == 2 {
			want = "changed"
		}
		if i != 1 && os.WriteFile(dir+"/input-put", []byte(want), 0o644) != nil {
			t.Fatal("input write failed")
			return
		}
		code, fresh := materializeSource(t, dir, source, true, false)
		if code != "" || fresh != (i == 1) || !tempMatches(t, dir+"/output", want) {
			t.Error("computed capture input failed execution or freshness: " + code)
		}
		mem.FreeString(a, code)
	}
}

func TestKameBodiesRejectEffectsInvalidValuesAndAmbiguousProducers(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-value-rejection-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/output", []byte("keep"), 0o644) != nil {
		t.Fatal("output write failed")
		return
	}
	bodies := []string{"42", "nil", "[1, 2]", "(x => x)", "(err! \"bad\")", "(out! \"bad\")", "(write! ./side \"bad\")", "$(touch side)", "(now ())", "VALUE", "(helper ())"}
	for i := range bodies {
		text := "VALUE = (sh! \"touch side\")\ndef helper () = (out! \"bad\")\nrule ./output = " + bodies[i] + "\n"
		code, _ := materializeSource(t, dir, text, true, false)
		want := "PHASE_INVALID"
		if i < 4 {
			want = "EXPR_INVALID"
		}
		if code != want {
			t.Error("unexpected rejection for " + bodies[i] + ": " + code)
		}
		mem.FreeString(a, code)
		if !tempMatches(t, dir+"/output", "keep") {
			t.Error("failed computation replaced the artifact")
		}
	}
	sources := []string{
		"rule ./output = \"one\"\n\t\"two\"\n",
		"rule ./output =\n",
		"rule ./output ./sibling = \"content\"\n",
		"rule ./output ; {shell: kame}\n\t\"content\"\n",
	}
	codes := []string{"PARSE_ERR", "PARSE_ERR", "EXPR_INVALID", "REF_MISSING"}
	for i := range sources {
		code, _ := materializeSource(t, dir, sources[i], true, false)
		if code != codes[i] {
			t.Errorf("invalid body mode %d returned %s, want %s", i, code, codes[i])
		}
		mem.FreeString(a, code)
	}
	data, readErr := os.ReadFile(dir + "/side")
	if readErr == nil {
		t.Error("rejected computation performed an effect")
	}
	mem.FreeSlice(a, data)
}
