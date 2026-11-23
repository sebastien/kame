package wasm

import (
	"fmt"
	"kame/internal/mem"
	"testing"
)

func TestWatchRotatingInputsWithoutSnapshotQueries(t *testing.T) {
	r := NewRuntime(mem.System, "VALUE = text (read (text (read ./selector)))\n").Runtime
	defer r.Free()
	r.SetFile("selector", []byte("./input-0"))
	filesystem := r.Host
	if begin := r.RequestWatch([]byte(`["VALUE"]`)); begin.Code != "" {
		t.Fatal(begin.Message)
	}
	for i := range 64 {
		path := fmt.Sprintf("input-%d", i%32)
		want := fmt.Sprint(i)
		filesystem.SetFile(path, []byte(want))
		filesystem.SetFile("selector", []byte("./"+path))
		if i != 0 {
			update := r.InvalidateWatch([]byte(`[{"kind":"file","name":"selector"}]`))
			if update.Code != "" {
				t.Fatal(update.Message)
			}
		}
		for tick := 0; tick < 64 && (!r.Handle.Node.Current || r.Handle.Node.Latest.Text != want); tick++ {
			r.Step()
		}
		if r.Handle.Node.Latest.Text != want {
			t.Fatalf("cycle %d: expected fresh contents of %s, snapshot: %s", i, path, r.WatchStateJSON().Text)
		}
		// Root, selector, selected input, and the last input awaiting collection.
		if n := len(r.Program.Engine.TrackedNodes()); n > 4 {
			t.Fatalf("cycle %d retained %d nodes", i, n)
		}
	}
}
