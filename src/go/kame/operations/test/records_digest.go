package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestRecordOperationsAndKindPredicates(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "records.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(eq (items [z: 1 a: [inner: [2 :nil]]]) [[key: "z" value: 1] [key: "a" value: [inner: [2 :nil]]]])`,
		`(eq (items (set-field [z: 1 a: 2] "z" 3)) [[key: "z" value: 3] [key: "a" value: 2]])`,
		`(eq (items (set-field [z: 1] "a" :nil)) [[key: "z" value: 1] [key: "a" value: :nil]])`,
		`(eq (merge) (parse-json "{}"))`,
		`(eq (items (merge [z: [one: 1] a: 2] [z: [two: 2] b: 3])) [[key: "z" value: [two: 2]] [key: "a" value: 2] [key: "b" value: 3]])`,
		`(eq (apply (get (first (items [f: ([x] (cat x "!"))])) "value") ["ok"]) "ok!")`,
		`(eq (apply (get (set-field [f: ([x] x)] "f" ([x] (cat x "!"))) "f") ["ok"]) "ok!")`,
		`(eq (apply (get (merge [f: ([x] x)] [f: ([x] (cat x "!"))]) "f") ["ok"]) "ok!")`,
		`(eq (apply (get (let [n 42] (set-field [old: ([x] x)] "f" ([x] n))) "f") []) 42)`,
		`(record? (parse-json "{}"))`, `(list? [])`, `(string? "")`,
		`(not (record? []))`, `(not (list? :nil))`, `(not (string? "{x:*}"))`,
		`(not (record? [([x] x)]))`, `(not (string? [f: ([x] x)]))`,
	}
	for i := range texts {
		result := evaluate(t, p, texts[i])
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || !result.Value.Bool { t.Error("record/predicate check failed: "+texts[i]) }
		result.Free(a)
	}
	invalid := []string{`(items [([x] x)])`, `(set-field [f: ([x] x)] ([x] x) 3)`, `(merge [f: ([x] x)] [([x] x)])`, `(digest "sha1" [f: ([x] x)])`}
	for i := range invalid {
		result := evaluate(t, p, invalid[i])
		if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("invalid native record/digest argument accepted") }
		result.Free(a)
	}
	engine.Free(); p.Free(); parsed.Free(); registry.Free()
}

func TestStandardDigestVectors(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "digests.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`(digest "sha1" "abc")`, `(digest "sha256" "abc")`, `(digest "sha512" "abc")`}
	expected := []string{
		"a9993e364706816aba3e25717850c26c9cd0d89d",
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
	}
	for i := range texts {
		result := evaluate(t, p, texts[i])
		if result.Diagnostic.Code != "" || result.Value.Text != expected[i] { t.Error("standard digest vector failed") }
		result.Free(a)
	}
	result := evaluate(t, p, `(digest "SHA-512" "abc")`)
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("unknown digest algorithm accepted") }
	result.Free(a)
	engine.Free(); p.Free(); parsed.Free(); registry.Free()
}
