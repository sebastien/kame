package usage

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// node is one KDL node. Values keep their text; #true, #false and #null keep
// their spelling, and v1 true/false/null are read as the v2 keywords.
type node struct {
	name     string
	args     []string
	props    map[string]string
	children []node
}

type kdlParser struct {
	text string
	pos  int
}

// parseKDL reads the KDL v2 document syntax Usage specs use, accepting the
// common v1 spellings. Type annotations are skipped.
func parseKDL(text string) ([]node, error) {
	p := &kdlParser{text: text}
	nodes, err := p.nodes(false)
	if err != nil {
		line := strings.Count(text[:min(p.pos, len(text))], "\n") + 1
		return nil, fmt.Errorf("line %d: %w", line, err)
	}
	return nodes, nil
}

func (p *kdlParser) nodes(nested bool) ([]node, error) {
	var nodes []node
	for {
		if err := p.space(true); err != nil {
			return nil, err
		}
		if p.pos >= len(p.text) {
			if nested {
				return nil, errors.New("unclosed children block")
			}
			return nodes, nil
		}
		if p.text[p.pos] == '}' {
			if !nested {
				return nil, errors.New("unexpected }")
			}
			p.pos++
			return nodes, nil
		}
		if p.text[p.pos] == ';' {
			p.pos++
			continue
		}
		skip, err := p.slashdash()
		if err != nil {
			return nil, err
		}
		n, err := p.node()
		if err != nil {
			return nil, err
		}
		if !skip {
			nodes = append(nodes, n)
		}
	}
}

func (p *kdlParser) slashdash() (bool, error) {
	if !strings.HasPrefix(p.text[p.pos:], "/-") {
		return false, nil
	}
	p.pos += 2
	if err := p.space(true); err != nil {
		return false, err
	}
	if p.pos == len(p.text) {
		return false, errors.New("expected a node or entry after /-")
	}
	return true, nil
}

func (p *kdlParser) node() (node, error) {
	p.annotation()
	name, ok, err := p.value()
	if err != nil {
		return node{}, err
	}
	if !ok {
		return node{}, fmt.Errorf("expected a node name, found %q", p.text[p.pos:p.pos+1])
	}
	n := node{name: name, props: map[string]string{}}
	for {
		if err := p.space(false); err != nil {
			return node{}, err
		}
		if p.pos >= len(p.text) {
			return n, nil
		}
		switch c := p.text[p.pos]; {
		case c == '\n' || c == '\r' || c == ';':
			return n, nil
		case c == '}':
			return n, nil
		}
		skip, err := p.slashdash()
		if err != nil {
			return node{}, err
		}
		if p.text[p.pos] == '{' {
			p.pos++
			children, err := p.nodes(true)
			if err != nil {
				return node{}, err
			}
			if !skip {
				n.children = append(n.children, children...)
			}
			continue
		}
		p.annotation()
		value, ok, err := p.value()
		if err != nil {
			return node{}, err
		}
		if !ok {
			return node{}, fmt.Errorf("unexpected %q", p.text[p.pos:p.pos+1])
		}
		if p.pos < len(p.text) && p.text[p.pos] == '=' {
			p.pos++
			p.annotation()
			v, ok, err := p.value()
			if err != nil {
				return node{}, err
			}
			if !ok {
				return node{}, errors.New("expected a property value")
			}
			if !skip {
				n.props[value] = v
			}
			continue
		}
		if !skip {
			n.args = append(n.args, value)
		}
	}
}

func (p *kdlParser) annotation() {
	if p.pos < len(p.text) && p.text[p.pos] == '(' {
		if end := strings.IndexByte(p.text[p.pos:], ')'); end >= 0 {
			p.pos += end + 1
		}
	}
}

// space skips blanks, comments and line continuations; newlines too when
// lines is set.
func (p *kdlParser) space(lines bool) error {
	for p.pos < len(p.text) {
		rest := p.text[p.pos:]
		switch {
		case rest[0] == ' ' || rest[0] == '\t':
			p.pos++
		case strings.HasPrefix(rest, "\xef\xbb\xbf"):
			p.pos += len("\xef\xbb\xbf")
		case lines && (rest[0] == '\n' || rest[0] == '\r'):
			p.pos++
		case strings.HasPrefix(rest, "//"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				end = len(rest)
			}
			p.pos += end
		case strings.HasPrefix(rest, "/*"):
			depth, i := 0, 0
			for ; i < len(rest); i++ {
				if strings.HasPrefix(rest[i:], "/*") {
					depth++
					i++
				} else if strings.HasPrefix(rest[i:], "*/") {
					depth--
					i++
					if depth == 0 {
						break
					}
				}
			}
			if depth != 0 {
				return errors.New("unclosed comment")
			}
			p.pos += i + 1
		case rest[0] == '\\':
			p.pos++
			p.space(false)
			if p.pos < len(p.text) && p.text[p.pos] == '\r' {
				p.pos++
			}
			if p.pos < len(p.text) && p.text[p.pos] == '\n' {
				p.pos++
			}
		default:
			return nil
		}
	}
	return nil
}

const kdlNonIdentifier = "\\/(){};[]\"#= \t\r\n"

// value reads a string, keyword, number or bare identifier.
func (p *kdlParser) value() (string, bool, error) {
	if p.pos >= len(p.text) {
		return "", false, errors.New("unexpected end of input")
	}
	rest := p.text[p.pos:]
	switch {
	case strings.HasPrefix(rest, `"""`):
		return p.multiline()
	case rest[0] == '"':
		return p.quoted()
	case rest[0] == '#' && len(rest) > 1 && (rest[1] == '"' || rest[1] == '#'):
		return p.raw(rest)
	case rest[0] == 'r' && len(rest) > 1 && (rest[1] == '"' || rest[1] == '#'):
		p.pos++
		return p.raw(rest[1:])
	case rest[0] == '#':
		end := 1
		for end < len(rest) && !strings.ContainsRune(kdlNonIdentifier, rune(rest[end])) {
			end++
		}
		p.pos += end
		return rest[:end], true, nil
	}
	end := 0
	for end < len(rest) {
		r, size := utf8.DecodeRuneInString(rest[end:])
		if r < 0x80 && strings.ContainsRune(kdlNonIdentifier, r) {
			break
		}
		end += size
	}
	if end == 0 {
		return "", false, nil
	}
	p.pos += end
	switch word := rest[:end]; word {
	case "true", "false", "null":
		return "#" + word, true, nil
	default:
		return word, true, nil
	}
}

func (p *kdlParser) quoted() (string, bool, error) {
	var b strings.Builder
	for i := p.pos + 1; i < len(p.text); i++ {
		switch c := p.text[i]; c {
		case '"':
			p.pos = i + 1
			return b.String(), true, nil
		case '\\':
			i++
			if i >= len(p.text) {
				break
			}
			switch e := p.text[i]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 's':
				b.WriteByte(' ')
			case 'u':
				end := strings.IndexByte(p.text[i:], '}')
				if !strings.HasPrefix(p.text[i:], "u{") || end < 0 {
					return "", false, errors.New("invalid unicode escape")
				}
				code, err := strconv.ParseUint(p.text[i+2:i+end], 16, 32)
				if err != nil {
					return "", false, errors.New("invalid unicode escape")
				}
				b.WriteRune(rune(code))
				i += end
			case ' ', '\t', '\n', '\r':
				// Whitespace escape: skip all following whitespace.
				for i+1 < len(p.text) && strings.ContainsRune(" \t\r\n", rune(p.text[i+1])) {
					i++
				}
			default:
				b.WriteByte(e)
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", false, errors.New("unclosed string")
}

func (p *kdlParser) raw(rest string) (string, bool, error) {
	hashes := len(rest) - len(strings.TrimLeft(rest, "#"))
	if hashes >= len(rest) || rest[hashes] != '"' {
		return "", false, errors.New("invalid raw string")
	}
	start := hashes + 1
	multi := strings.HasPrefix(rest[hashes:], `"""`)
	if multi {
		start = hashes + 3
	}
	closing := `"` + strings.Repeat("#", hashes)
	if multi {
		closing = `"""` + strings.Repeat("#", hashes)
	}
	end := strings.Index(rest[start:], closing)
	if end < 0 {
		return "", false, errors.New("unclosed raw string")
	}
	p.pos += start + end + len(closing)
	body := rest[start : start+end]
	if multi {
		body = dedent(body)
	}
	return body, true, nil
}

func (p *kdlParser) multiline() (string, bool, error) {
	end := strings.Index(p.text[p.pos+3:], `"""`)
	if end < 0 {
		return "", false, errors.New("unclosed multi-line string")
	}
	body := p.text[p.pos+3 : p.pos+3+end]
	p.pos += 3 + end + 3
	// Escapes in multi-line strings are rare in specs; keep the text.
	return dedent(body), true, nil
}

// dedent drops the first and last lines of a multi-line string body and the
// last line's indentation from every line.
func dedent(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		return body
	}
	indent := lines[len(lines)-1]
	lines = lines[1 : len(lines)-1]
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], indent)
	}
	return strings.Join(lines, "\n")
}

// ParseKDL reads a Usage spec. Nodes outside the completion model are
// ignored; hidden commands, flags and arguments are left out.
func ParseKDL(text string) (Spec, error) {
	nodes, err := parseKDL(text)
	if err != nil {
		return Spec{}, err
	}
	var spec Spec
	readCommand(&spec, nodes, nil, true)
	return spec, nil
}

func hidden(n node) bool { return n.props["hide"] == "#true" }

// readCommand fills spec from a command body. Named complete nodes apply to
// arguments of that name in this command, then in enclosing ones.
func readCommand(spec *Spec, nodes []node, outer map[string]string, root bool) {
	named := map[string]string{}
	for k, v := range outer {
		named[k] = v
	}
	for _, n := range nodes {
		if n.name == "complete" && len(n.args) != 0 {
			named[n.args[0]] = completeType(n)
		}
	}
	for _, n := range nodes {
		if hidden(n) {
			continue
		}
		switch n.name {
		case "bin":
			if root && len(n.args) != 0 {
				spec.Name = n.args[0]
			}
		case "about":
			if root && len(n.args) != 0 && spec.Help == "" {
				spec.Help = n.args[0]
			}
		case "alias":
			if !root {
				spec.Aliases = append(spec.Aliases, n.args...)
			}
		case "flag":
			if f, ok := readFlag(n, named); ok {
				spec.Flags = append(spec.Flags, f)
			}
		case "arg":
			if len(n.args) != 0 {
				spec.Args = append(spec.Args, readArg(n, named))
			}
		case "cmd":
			if len(n.args) != 0 {
				sub := Spec{Name: n.args[0], Help: n.props["help"]}
				readCommand(&sub, n.children, named, false)
				spec.Commands = append(spec.Commands, sub)
			}
		}
	}
}

// completeType maps a Usage completer to file, dir or none; a run=
// completer is never executed and leaves the default.
func completeType(n node) string {
	switch t := n.props["type"]; {
	case t == "dir" || t == "directory":
		return "dir"
	case t == "file" || t == "path" || strings.HasPrefix(t, "path:") || strings.HasPrefix(t, "file:"):
		return "file"
	case t == "none":
		return "none"
	}
	return ""
}

func readChoicesAndComplete(children []node) (choices []string, complete string) {
	for _, c := range children {
		switch c.name {
		case "choices":
			choices = append(choices, c.args...)
		case "complete":
			complete = completeType(c)
		}
	}
	return choices, complete
}

func readFlag(n node, named map[string]string) (Flag, bool) {
	if len(n.args) == 0 {
		return Flag{}, false
	}
	f := Flag{Help: n.props["help"], Global: n.props["global"] == "#true"}
	for _, field := range strings.Fields(strings.ReplaceAll(n.args[0], ",", " ")) {
		name, value, _ := strings.Cut(field, "=")
		switch {
		case strings.HasPrefix(name, "-") && len(name) > 1:
			f.Names = append(f.Names, name)
			if value != "" {
				f.Value, f.Optional = strings.Trim(value, "<>[]"), strings.HasPrefix(value, "[")
			}
		case strings.HasPrefix(field, "<"):
			f.Value = strings.Trim(field, "<>.")
		case strings.HasPrefix(field, "["):
			f.Value, f.Optional = strings.Trim(field, "[]."), true
		}
	}
	f.Choices, f.Complete = readChoicesAndComplete(n.children)
	for _, c := range n.children {
		if c.name == "arg" && len(c.args) != 0 {
			arg := readArg(c, named)
			if f.Value == "" {
				f.Value, f.Optional = arg.Name, arg.Optional
			}
			f.Choices = append(f.Choices, arg.Choices...)
			if f.Complete == "" {
				f.Complete = arg.Complete
			}
		}
	}
	if f.Complete == "" && f.Value != "" {
		f.Complete = named[f.Value]
	}
	return f, len(f.Names) != 0
}

func readArg(n node, named map[string]string) Arg {
	text := n.args[0]
	a := Arg{Help: n.props["help"], Variadic: n.props["var"] == "#true" || strings.HasSuffix(text, "...")}
	text = strings.TrimSuffix(text, "...")
	a.Optional = strings.HasPrefix(text, "[") || n.props["required"] == "#false"
	a.Name = strings.Trim(text, "<>[]")
	a.Choices, a.Complete = readChoicesAndComplete(n.children)
	if a.Complete == "" {
		a.Complete = named[a.Name]
	}
	return a
}

// RenderKDL writes a spec as canonical Usage KDL.
func RenderKDL(spec Spec) string {
	var b strings.Builder
	if spec.Name != "" {
		fmt.Fprintf(&b, "bin %s\n", kdlString(spec.Name))
	}
	if spec.Help != "" {
		fmt.Fprintf(&b, "about %s\n", kdlString(spec.Help))
	}
	renderBody(&b, spec, "")
	return b.String()
}

func renderBody(b *strings.Builder, spec Spec, indent string) {
	if len(spec.Aliases) != 0 {
		fmt.Fprintf(b, "%salias %s\n", indent, kdlStrings(spec.Aliases))
	}
	for _, f := range spec.Flags {
		text := strings.Join(f.Names, " ")
		if f.Value != "" {
			if f.Optional {
				text += " [" + f.Value + "]"
			} else {
				text += " <" + f.Value + ">"
			}
		}
		fmt.Fprintf(b, "%sflag %s", indent, kdlString(text))
		if f.Help != "" {
			fmt.Fprintf(b, " help=%s", kdlString(f.Help))
		}
		if f.Global {
			b.WriteString(" global=#true")
		}
		renderValues(b, f.Choices, f.Complete, indent)
	}
	for _, a := range spec.Args {
		text := "<" + a.Name + ">"
		if a.Optional {
			text = "[" + a.Name + "]"
		}
		fmt.Fprintf(b, "%sarg %s", indent, kdlString(text))
		if a.Help != "" {
			fmt.Fprintf(b, " help=%s", kdlString(a.Help))
		}
		if a.Variadic {
			b.WriteString(" var=#true")
		}
		renderValues(b, a.Choices, a.Complete, indent)
	}
	for _, c := range spec.Commands {
		fmt.Fprintf(b, "%scmd %s", indent, kdlString(c.Name))
		if c.Help != "" {
			fmt.Fprintf(b, " help=%s", kdlString(c.Help))
		}
		if len(c.Aliases)+len(c.Flags)+len(c.Args)+len(c.Commands) == 0 {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(" {\n")
		renderBody(b, c, indent+"    ")
		fmt.Fprintf(b, "%s}\n", indent)
	}
}

func renderValues(b *strings.Builder, choices []string, complete, indent string) {
	if len(choices) == 0 && complete == "" {
		b.WriteByte('\n')
		return
	}
	b.WriteString(" {\n")
	if len(choices) != 0 {
		fmt.Fprintf(b, "%s    choices %s\n", indent, kdlStrings(choices))
	}
	if complete != "" {
		fmt.Fprintf(b, "%s    complete type=%s\n", indent, kdlString(complete))
	}
	fmt.Fprintf(b, "%s}\n", indent)
}

func kdlStrings(values []string) string {
	quoted := slices.Clone(values)
	for i := range quoted {
		quoted[i] = kdlString(quoted[i])
	}
	return strings.Join(quoted, " ")
}

func kdlString(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u{%x}`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
