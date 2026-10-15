package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestSubjectLastOperationsAndPipes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "order.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	checks := []string{
		`(eq (nth 1 [10 20]) ((nop [10 20]) | nth 1))`,
		`(eq ("aéz" | nth -2) "é")`,
		`(eq (get "x" [x: 7]) ((nop [x: 7]) | get "x"))`,
		`(eq (get "x" 42 [y: 0]) ((nop [y: 0]) | get "x" 42))`,
		`(eq ((nop [x: :nil]) | get "x" 42) :nil)`,
		`(eq (get "missing" [default: 1] [other: 2]) [default: 1])`,
		`(eq ((nop [x: 1]) | set-field "x" 2 | get "x") 2)`,
		`(eq (slice 1 "aéz") ("aéz" | slice 1))`,
		`(eq (slice 1 -1 [0 1 2 3]) ((nop [0 1 2 3]) | slice 1 -1))`,
		`(eq ("a-b-c" | split "-" | join ":") "a:b:c")`,
		`(eq (join ":" ["a" "b"]) ((nop ["a" "b"]) | join ":"))`,
		`(eq (split "-" "a-b") ("a-b" | split "-"))`,
		`(eq (sub "a" "x" "a-b-a") ("a-b-a" | sub "a" "x"))`,
		`(eq ("banana" | replace "a" "b") "bbnbnb")`,
		`(includes? "b" "abc")`, `(starts? "ab" "abc")`, `(ends? "bc" "abc")`,
		`("abc" | includes? "b")`, `("abc" | starts? "ab")`, `("abc" | ends? "bc")`,
		`(eq (map uppercase ["a" "b"]) ((nop ["a" "b"]) | map uppercase))`,
		`(eq ((nop [1 2]) | flatmap ([x] [x x])) [1 1 2 2])`,
		`(eq ((nop [1 2 1]) | filter 1) [1 1])`,
		`(eq ((nop [[1] [2] [1]]) | filter [1]) [[1] [1]])`,
		`(eq ((nop [1 2 1]) | filter-out 1) [2])`,
		`(eq ((nop ["a" "b"]) | filter ((includes? "a" _))) ["a"])`,
		`(eq (reduce str "z" ["a" "b"]) ((nop ["a" "b"]) | reduce str "z"))`,
		`(eq ((nop ["a" "b"]) | reduce str "z") "zab")`,
		`(eq ((nop ["a" "b"]) | reduce str) "ab")`,
		`(eq (reduce ([acc item] (concat acc [item])) [9] [1 2]) [9 1 2])`,
		`(eq ((list) | reduce str "z") "z")`, `(eq ((list) | reduce str) :nil)`,
		`(eq ((nop ["a" "b"]) | apply ([x y] (str x y))) "ab")`,
		`(eq (relpath "a" "a/b") ("a/b" | relpath "a"))`,
		`(eq ("a/b" | relpath "a") "b")`,
		`(eq (render [name: "Ada"] "plain" """Hi @(name)""") ("""Hi @(name)""" | render [name: "Ada"] "plain"))`,
		`(eq (render "plain" "hello") "hello")`,
		`(eq (render [name: "Ada"] """Hi @(name)""") "Hi Ada")`,
		`(eq ((nop [[[7]]]) | nth 0 | nth 0 | nth 0) (nth 0 (nth 0 (nth 0 [[[7]]]))))`,
		`(eq (let [at nth] ((nop [4 5]) | at 1)) 5)`,
		`(gt 3 2)`, `(lt 2 3)`, `(gte 3 3)`, `(lte 3 3)`,
		`(eq (2 | gt 3) :true)`, `(eq (2 | gt _ 3) :false)`,
		`(eq (merge [x: 1] [x: 2]) [x: 2])`,
		`(eq (concat [1] [2]) [1 2])`,
	}
	for i := range checks {
		r := evaluate(t, p, checks[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || !r.Value.Bool {
			t.Error("subject-last check failed: " + checks[i])
		}
		r.Free(a)
	}
	invalid := []string{
		`(nth [1] 0)`, `(get [x: 1] "x")`, `(get [x: 1] "x" 7)`,
		`(set-field [x: 1] "x" 2)`, `(slice [1] 0)`, `(slice [1] 0 1)`,
		`(join ["a"] "-")`, `(map [1] ([x] x))`, `(flatmap [1] ([x] [x]))`,
		`(flatmap "first" ([x] [x]))`,
		`(filter [1] ([x] :true))`, `(filter-out [1] ([x] :true))`,
		`(reduce str ["a"] "")`, `(apply [1] ([x] x))`,
		`(render "hello" [name: "Ada"])`,
	}
	for i := range invalid {
		r := evaluate(t, p, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("former argument order accepted: " + invalid[i]) }
		r.Free(a)
	}
	engine.Free(); p.Free(); parsed.Free(); registry.Free()
}
