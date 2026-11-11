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
		"SHELL = kash\n./output : ./input ; [shell: kame]\n\t(str \"Hello, \"\n\t  (strip (text (read @<))) \"!\\n\")\n",
		"./output : ./input ; [shell: kame]\n\t(read @<)\n",
		"./output : ./input ; [shell: kame]\n\t\"\"\n",
		"./output : ./input ; [shell: kame]\n\t\"\"\"first\n\t  second\n\t\"\"\"\n",
		"./output : ./input ; [shell: kame]\n\t\"first\n\t  second\n\t\"\n",
		"./output : ./input ; [shell: kame]\n    \"\"\"first\n      second\n    \"\"\"\n",
		"./output : ./input ; [shell: kame]\n\t\"\"\"\n\t@if(:false)\n\tdata\n\t@end\n\t\"\"\"\n",
		"SHELL = kame\n./output : ./input\n\t(read @<)\n",
		"MODE = kame\n./output : ./input ; [shell: MODE]\n\t(read @<)\n",
	}
	wants := []string{"Hello, Ada!\n", "Ada\n", "", "first\n  second\n", "first\n  second\n", "first\n  second\n", "\n@if(:false)\ndata\n@end\n", "Ada\n", "Ada\n"}
	for i := range sources {
		if code, _ := materializeSource(t, dir, sources[i], true, false); code != "" {
			t.Error("Kame body failed: " + code)
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
	source := "ROOT = \".\"\n./out{path} : @{ROOT}/input-{path} ; [shell: kame]\n\t(read @<)\n"
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
	bodies := []string{"42", ":nil", "[1 2]", "([x] x)", "(yield! \"bad\")", "(out! \"bad\")", "(write! ./side \"bad\")", "$(touch side)", "(now)", "VALUE", "(helper)"}
	for i := range bodies {
		text := "VALUE = (sh! \"touch side\")\n(helper) = (out! \"bad\")\n./output : ; [shell: kame]\n\t" + bodies[i] + "\n"
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
		"./output : ; [shell: kame]\n\t\"one\"\n\t\"two\"\n",
		"./output : ; [shell: kame]\n",
		"./output ./sibling : ; [shell: kame]\n\t\"content\"\n",
	}
	for i := range sources {
		code, _ := materializeSource(t, dir, sources[i], true, false)
		want := "EXPR_INVALID"
		if i < 2 {
			want = "PARSE_ERR"
		}
		if code != want {
			t.Error("invalid body mode accepted: " + code)
		}
		mem.FreeString(a, code)
	}
	data, readErr := os.ReadFile(dir + "/side")
	if readErr == nil {
		t.Error("rejected computation performed an effect")
	}
	mem.FreeSlice(a, data)
}
