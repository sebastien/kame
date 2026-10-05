package expr

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type regexNodeKind int

const (
	regexByteNode regexNodeKind = iota
	regexAnyNode
	regexClassNode
	regexSequenceNode
	regexAlternativeNode
	regexOptionalNode
	regexZeroOrMoreNode
	regexOneOrMoreNode
)

type regexNode struct {
	Kind       regexNodeKind
	Byte       byte
	Left, Right int
	Start, End int
}

type regexParser struct {
	a      mem.Allocator
	text   string
	pos    int
	depth  int
	nodes  []regexNode
	error  int
	failed bool
}

type regexOp int

const (
	regexConsumeByte regexOp = iota
	regexConsumeAny
	regexConsumeClass
	regexSplit
	regexAccept
)

type regexInstruction struct {
	Op          regexOp
	Byte        byte
	Start, End  int
	Out1, Out2  int
}

type regexProgram struct {
	Alloc        mem.Allocator
	Text         string
	Instructions []regexInstruction
	Start        int
}

type regexCompileResult struct {
	Program     *regexProgram
	ErrorOffset int
	Valid       bool
}

const maxRegexBytes = 256
const RegexStepLimit = 1000000
const maxRegexSteps = RegexStepLimit

func (p *regexProgram) Free(a mem.Allocator) {
	if p == nil { return }
	slices.Free(a, p.Instructions)
	mem.Free(a, p)
}

func compileRegex(a mem.Allocator, text string) regexCompileResult {
	if len(text) == 0 || len(text) > maxRegexBytes { return regexCompileResult{ErrorOffset: 0} }
	parser := regexParser{a: a, text: text, error: -1}
	root := parser.alternative()
	if parser.failed || parser.pos != len(text) || root < 0 {
		at := parser.error
		if at < 0 { at = parser.pos }
		slices.Free(a, parser.nodes)
		return regexCompileResult{ErrorOffset: at}
	}
	accept := slices.Append(a, []regexInstruction(nil), regexInstruction{Op: regexAccept, Out1: -1, Out2: -1})
	program := mem.Alloc[regexProgram](a)
	program.Alloc = a
	program.Text = text
	program.Instructions = accept
	program.Start = compileRegexNode(a, parser.nodes, root, 0, &program.Instructions)
	slices.Free(a, parser.nodes)
	return regexCompileResult{Program: program, ErrorOffset: -1, Valid: true}
}

func (p *regexParser) fail(at int) {
	if !p.failed { p.error = at }
	p.failed = true
}

func (p *regexParser) add(node regexNode) int {
	p.nodes = slices.Append(p.a, p.nodes, node)
	return len(p.nodes) - 1
}

func (p *regexParser) alternative() int {
	left := p.sequence()
	if left < 0 { return -1 }
	for p.pos < len(p.text) && p.text[p.pos] == '|' {
		bar := p.pos
		p.pos++
		right := p.sequence()
		if right < 0 { p.fail(bar); return -1 }
		left = p.add(regexNode{Kind: regexAlternativeNode, Left: left, Right: right})
	}
	return left
}

func (p *regexParser) sequence() int {
	if p.pos >= len(p.text) || p.text[p.pos] == '|' || p.text[p.pos] == ')' { return -1 }
	left := p.repetition()
	if left < 0 { return -1 }
	for p.pos < len(p.text) && p.text[p.pos] != '|' && p.text[p.pos] != ')' {
		right := p.repetition()
		if right < 0 { return -1 }
		left = p.add(regexNode{Kind: regexSequenceNode, Left: left, Right: right})
	}
	return left
}

func (p *regexParser) repetition() int {
	atom := p.atom()
	if atom < 0 { return -1 }
	if p.pos < len(p.text) {
		kind := regexNodeKind(-1)
		switch p.text[p.pos] {
		case '?': kind = regexOptionalNode
		case '*': kind = regexZeroOrMoreNode
		case '+': kind = regexOneOrMoreNode
		}
		if kind >= 0 {
			p.pos++
			atom = p.add(regexNode{Kind: kind, Left: atom})
			if p.pos < len(p.text) && (p.text[p.pos] == '?' || p.text[p.pos] == '*' || p.text[p.pos] == '+') { p.fail(p.pos); return -1 }
		}
	}
	return atom
}

func (p *regexParser) atom() int {
	if p.pos >= len(p.text) { p.fail(p.pos); return -1 }
	start := p.pos
	b := p.text[p.pos]
	p.pos++
	switch b {
	case '(':
		p.depth++
		if p.depth > 64 { p.fail(start); return -1 }
		child := p.alternative()
		p.depth--
		if child < 0 || p.pos >= len(p.text) || p.text[p.pos] != ')' { p.fail(start); return -1 }
		p.pos++
		return child
	case ')', '|', '?', '*', '+', '{', '}':
		p.fail(start)
		return -1
	case '.':
		return p.add(regexNode{Kind: regexAnyNode})
	case '[':
		end, ok := regexClassEnd(p.text, start)
		if !ok || !regexClassValid(p.text[start+1:end]) { p.fail(start); return -1 }
		p.pos = end + 1
		return p.add(regexNode{Kind: regexClassNode, Start: start + 1, End: end})
	case '\\':
		if p.pos >= len(p.text) { p.fail(start); return -1 }
		b = p.text[p.pos]
		p.pos++
		if (b == 'p' || b == 'P') && p.pos < len(p.text) && p.text[p.pos] == '{' { p.fail(start); return -1 }
	}
	return p.add(regexNode{Kind: regexByteNode, Byte: b})
}

func regexClassEnd(text string, start int) (int, bool) {
	i := start + 1
	if i < len(text) && text[i] == '!' { i++ }
	contentStart := i
	for i < len(text) {
		if text[i] == '\\' {
			i += 2
			continue
		}
		if text[i] == ']' {
			return i, i > contentStart
		}
		i++
	}
	return 0, false
}

func regexClassValid(class string) bool {
	if len(class) == 0 { return false }
	negate := class[0] == '!'
	i := 0
	if negate { i++ }
	for i < len(class) {
		if class[i] == '{' || class[i] == '}' { return false }
		first := class[i]
		if first == '\\' {
			i++
			if i == len(class) { return false }
			first = class[i]
		}
		i++
		if i+1 < len(class) && class[i] == '-' {
			i++
			last := class[i]
			if last == '\\' {
				i++
				if i == len(class) { return false }
				last = class[i]
			}
			if first > last { return false }
			i++
		}
	}
	return true
}

func compileRegexNode(a mem.Allocator, nodes []regexNode, index int, next int, instructions *[]regexInstruction) int {
	node := nodes[index]
	switch node.Kind {
	case regexByteNode:
		return appendRegexInstruction(a, instructions, regexInstruction{Op: regexConsumeByte, Byte: node.Byte, Out1: next, Out2: -1})
	case regexAnyNode:
		return appendRegexInstruction(a, instructions, regexInstruction{Op: regexConsumeAny, Out1: next, Out2: -1})
	case regexClassNode:
		return appendRegexInstruction(a, instructions, regexInstruction{Op: regexConsumeClass, Start: node.Start, End: node.End, Out1: next, Out2: -1})
	case regexSequenceNode:
		right := compileRegexNode(a, nodes, node.Right, next, instructions)
		return compileRegexNode(a, nodes, node.Left, right, instructions)
	case regexAlternativeNode:
		left := compileRegexNode(a, nodes, node.Left, next, instructions)
		right := compileRegexNode(a, nodes, node.Right, next, instructions)
		return appendRegexInstruction(a, instructions, regexInstruction{Op: regexSplit, Out1: left, Out2: right})
	case regexOptionalNode:
		child := compileRegexNode(a, nodes, node.Left, next, instructions)
		return appendRegexInstruction(a, instructions, regexInstruction{Op: regexSplit, Out1: child, Out2: next})
	case regexZeroOrMoreNode:
		split := appendRegexInstruction(a, instructions, regexInstruction{Op: regexSplit, Out1: -1, Out2: next})
		child := compileRegexNode(a, nodes, node.Left, split, instructions)
		(*instructions)[split].Out1 = child
		return split
	case regexOneOrMoreNode:
		split := appendRegexInstruction(a, instructions, regexInstruction{Op: regexSplit, Out1: -1, Out2: next})
		child := compileRegexNode(a, nodes, node.Left, split, instructions)
		(*instructions)[split].Out1 = child
		return child
	}
	return next
}

func appendRegexInstruction(a mem.Allocator, instructions *[]regexInstruction, inst regexInstruction) int {
	*instructions = slices.Append(a, *instructions, inst)
	return len(*instructions) - 1
}

func regexFullMatch(a mem.Allocator, program *regexProgram, text string, budget *int) (bool, bool) {
	currentBuffer := slices.Make[int](a, len(program.Instructions))
	nextBuffer := slices.Make[int](a, len(program.Instructions))
	current, next := currentBuffer, nextBuffer
	seen := slices.Make[bool](a, len(program.Instructions))
	defer slices.Free(a, currentBuffer)
	defer slices.Free(a, nextBuffer)
	defer slices.Free(a, seen)
	currentCount, nextCount := 0, 0
	if !regexAddState(program, program.Start, current, &currentCount, seen, budget) { return false, true }
	for i := 0; i < len(text); i++ {
		for j := range seen { seen[j] = false }
		nextCount = 0
		for j := 0; j < currentCount; j++ {
			if !regexSpend(budget) { return false, true }
			inst := program.Instructions[current[j]]
			if regexInstructionMatches(program.Text, inst, text[i]) {
				if !regexAddState(program, inst.Out1, next, &nextCount, seen, budget) { return false, true }
			}
		}
		current, next = next, current
		currentCount, nextCount = nextCount, currentCount
	}
	for i := 0; i < currentCount; i++ {
		if program.Instructions[current[i]].Op == regexAccept { return true, false }
	}
	return false, false
}

func regexSpend(budget *int) bool {
	*budget--
	return *budget >= 0
}

func regexAddState(program *regexProgram, index int, states []int, count *int, seen []bool, budget *int) bool {
	if index < 0 || index >= len(program.Instructions) || seen[index] { return true }
	seen[index] = true
	if !regexSpend(budget) { return false }
	inst := program.Instructions[index]
	if inst.Op == regexSplit {
		return regexAddState(program, inst.Out1, states, count, seen, budget) && regexAddState(program, inst.Out2, states, count, seen, budget)
	}
	if *count == len(states) { return false }
	states[*count] = index
	*count++
	return true
}

func regexInstructionMatches(text string, inst regexInstruction, value byte) bool {
	switch inst.Op {
	case regexConsumeByte:
		return inst.Byte == value
	case regexConsumeAny:
		return value != '/'
	case regexConsumeClass:
		return regexClassMatches(text[inst.Start:inst.End], value)
	}
	return false
}

func regexClassMatches(class string, value byte) bool {
	negate, i, found := false, 0, false
	if len(class) > 0 && class[0] == '!' { negate, i = true, 1 }
	for i < len(class) {
		first := class[i]
		if first == '\\' && i+1 < len(class) { i++; first = class[i] }
		i++
		if i+1 < len(class) && class[i] == '-' {
			i++
			last := class[i]
			if last == '\\' && i+1 < len(class) { i++; last = class[i] }
			i++
			if first <= value && value <= last { found = true }
		} else if first == value { found = true }
	}
	if negate { return !found }
	return found
}
