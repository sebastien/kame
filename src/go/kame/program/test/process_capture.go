package program_test

import (
	"kame/core"
	"kame/host"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestRecipeCapturesReuseDeclaredInputsAcrossInvocations(t *testing.T) {
	a := t.Allocator()
	captures := []string{
		"$(/bin/sh -c \"printf x >> capture-log; cat input\")",
		"(capture! [stdout: :true] \"/bin/sh\" \"-c\" \"printf x >> capture-log; cat input\")",
	}
	for kind := range captures {
		for scope := 0; scope < 3; scope++ {
			dirBuffer := make([]byte, os.MaxPathLen)
			dir, err := os.MkdirTemp(dirBuffer, "", "kame-recipe-capture-")
			if err != nil {
				t.Fatal("temporary directory failed")
				return
			}
			definition, value := "", captures[kind]
			if scope == 1 {
				definition, value = "(probe) = "+value+"\n", "(probe)"
			} else if scope == 2 {
				definition, value = "probe = "+value+"\n", "probe"
			}
			body := "\tlet captured = " + value + "\n"
			if kind == 0 {
				body += "\t(yield! captured)\n"
			} else {
				body += "\t(yield! captured.stdout)\n"
			}
			source := definition + "./output : ./input ; [shell: kash]\n" + body + "\t/bin/sh -c \"printf x >> recipe-log\"\n"
			captureLog, recipeLog := "", ""
			phases := 4
			if scope == 2 {
				phases = 2
			}
			for phase := 0; phase < phases; phase++ {
				want := "first"
				if phase >= 2 {
					want = "changed"
				}
				if phase%2 == 0 && os.WriteFile(dir+"/input", []byte(want), 0o644) != nil {
					t.Fatal("input write failed")
					return
				}
				parsed := script.Parse(a, "capture.kmk", source)
				registry := eval.NewRegistry(a)
				operations.Register(registry)
				compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, ResolveTool: posix.ResolveTool, Grants: []eval.Grant{{Capability: eval.Run}, {Capability: eval.Read, Names: []string{"."}}, {Capability: eval.Write, Names: []string{"."}}}})
				if compiled.Program == nil {
					compiled.Free(a)
					parsed.Free()
					registry.Free()
					t.Fatal("compile capture recipe")
					return
				}
				r := compiled.Program.Materialize("./output")
				fresh := phase%2 == 1
				if r.Diagnostic.Code != "" || r.Fresh != fresh {
					t.Errorf("kind %d scope %d phase %d: %s, fresh=%t want %t", kind, scope, phase, r.Diagnostic.Code, r.Fresh, fresh)
				}
				r.Free(a)
				compiled.Program.Free()
				compiled.Free(a)
				parsed.Free()
				registry.Free()
				if !fresh || scope == 2 {
					captureLog += "x"
				}
				if !fresh {
					recipeLog += "x"
				}
				if !tempMatches(t, dir+"/output", want) || !tempMatches(t, dir+"/capture-log", captureLog) || !tempMatches(t, dir+"/recipe-log", recipeLog) {
					t.Errorf("kind %d scope %d phase %d: unexpected output or process execution", kind, scope, phase)
				}
			}
			os.Remove(dir)
		}
	}
}

func TestTypedCaptureRepeatedCompletionOwnsBothStreams(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "capture.km", "probe = (capture! [stdout: :true stderr: :true] :accept-exit [1] \"/bin/sh\" \"-c\" \"printf out; printf err >&2; exit 1\")\n")
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ResolveTool: posix.ResolveTool, Grants: []eval.Grant{{Capability: eval.Run}}})
	if compiled.Program == nil {
		t.Fatal("compile typed capture")
		return
	}
	p := compiled.Program
	for i := 0; i < 8; i++ {
		if i != 0 {
			p.Engine.Invalidate(p.Eval.Definition("probe"))
		}
		r := p.Materialize("probe")
		if r.Diagnostic.Code != "" || host.PayloadInt(r.Value, "status") != 1 || host.EffectiveStatus(r.Value) != 0 || !host.PayloadBool(r.Value, "stdoutCaptured") || !host.PayloadBool(r.Value, "stderrCaptured") {
			t.Error("captured completion lost acceptance or capture flags")
		}
		stdout, stderr := core.Value{}, core.Value{}
		for j := range r.Value.Record {
			if r.Value.Record[j].Key == "stdout" {
				stdout = r.Value.Record[j].Value
			}
			if r.Value.Record[j].Key == "stderr" {
				stderr = r.Value.Record[j].Value
			}
		}
		if stdout.Kind != core.Bytes || stderr.Kind != core.Bytes || string(stdout.Bytes) != "out" || string(stderr.Bytes) != "err" {
			t.Error("captured streams were decoded, replayed or lost")
		}
		r.Free(a)
	}
	p.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
