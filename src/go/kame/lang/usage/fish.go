package usage

import (
	"errors"
	"slices"
	"strings"
)

// ParseFish reads the static completions for command from a fish completion
// file without running it. Only top-level literal complete declarations are
// used; a declaration with a variable, command substitution, condition or
// unsupported option is skipped whole. A file whose statements cannot be
// split reliably yields an error.
func ParseFish(command, text string) (Spec, error) {
	statements, err := fishStatements(text)
	if err != nil {
		return Spec{}, err
	}
	spec := Spec{Name: command}
	depth := 0
	for _, words := range statements {
		switch words[0].text {
		case "function", "if", "for", "while", "switch", "begin":
			depth++
			continue
		case "end":
			depth = max(0, depth-1)
			continue
		}
		if depth != 0 || words[0].text != "complete" {
			continue
		}
		d, ok := fishDeclaration(words[1:])
		if ok && d.command == command {
			d.apply(&spec)
		}
	}
	return spec, nil
}

type fishWord struct {
	text    string
	dynamic bool // has an expansion: $, ( ), braces or a glob
}

// fishStatements splits text into statements of words, honoring quotes,
// escapes, comments and line continuations.
func fishStatements(text string) ([][]fishWord, error) {
	var statements [][]fishWord
	var words []fishWord
	var word strings.Builder
	inWord, dynamic := false, false
	end := func() {
		if inWord {
			words = append(words, fishWord{text: word.String(), dynamic: dynamic})
		}
		word.Reset()
		inWord, dynamic = false, false
	}
	endStatement := func() {
		end()
		if len(words) != 0 {
			statements = append(statements, words)
		}
		words = nil
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '#' && !inWord:
			for i < len(text) && text[i] != '\n' {
				i++
			}
			endStatement()
		case c == '\n' || c == ';':
			endStatement()
		case c == ' ' || c == '\t' || c == '\r':
			end()
		case c == '|' || c == '&' || c == '<' || c == '>':
			// Pipelines and redirections are not declarations.
			end()
			words = append(words, fishWord{text: string(c), dynamic: true})
		case c == '\\':
			if i+1 >= len(text) {
				return nil, errors.New("trailing backslash")
			}
			i++
			switch text[i] {
			case '\n':
				end()
			case 'n':
				word.WriteByte('\n')
				inWord = true
			case 't':
				word.WriteByte('\t')
				inWord = true
			default:
				word.WriteByte(text[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			for i++; ; i++ {
				if i >= len(text) {
					return nil, errors.New("unclosed quote")
				}
				if text[i] == '\\' && i+1 < len(text) && (text[i+1] == '\'' || text[i+1] == '\\') {
					i++
				} else if text[i] == '\'' {
					break
				}
				word.WriteByte(text[i])
			}
		case c == '"':
			inWord = true
			for i++; ; i++ {
				if i >= len(text) {
					return nil, errors.New("unclosed quote")
				}
				if text[i] == '\\' && i+1 < len(text) && strings.IndexByte("\"\\$\n", text[i+1]) >= 0 {
					i++
				} else if text[i] == '"' {
					break
				} else if text[i] == '$' || text[i] == '(' {
					dynamic = true
				}
				word.WriteByte(text[i])
			}
		default:
			if strings.IndexByte("$(){}*?~", c) >= 0 {
				dynamic = true
			}
			word.WriteByte(c)
			inWord = true
		}
	}
	endStatement()
	return statements, nil
}

type fishDecl struct {
	command                              string
	shorts, longs, olds                  []string
	description                          string
	arguments                            []string
	requires, noFiles, forceFiles, valid bool
}

// fishOptionArgument lists complete options that take an argument.
var fishOptionArgument = map[string]string{
	"c": "command", "s": "short-option", "l": "long-option", "o": "old-option", "d": "description",
	"a": "arguments", "n": "condition", "w": "wraps", "p": "path", "C": "do-complete",
}

var fishSwitch = map[string]string{
	"r": "require-parameter", "f": "no-files", "F": "force-files", "x": "exclusive", "k": "keep-order",
	"e": "erase", "h": "help",
}

func fishDeclaration(words []fishWord) (fishDecl, bool) {
	d := fishDecl{valid: true}
	set := func(option string, value fishWord) {
		if value.dynamic {
			d.valid = false
		}
		switch option {
		case "command":
			d.command = value.text
		case "short-option":
			d.shorts = append(d.shorts, value.text)
		case "long-option":
			d.longs = append(d.longs, value.text)
		case "old-option":
			d.olds = append(d.olds, value.text)
		case "description":
			d.description = value.text
		case "arguments":
			// fish expands arguments when completing, whatever their quoting.
			if strings.ContainsAny(value.text, "$(") {
				d.valid = false
			}
			d.arguments = append(d.arguments, strings.Fields(value.text)...)
		default: // condition, wraps, path, do-complete
			d.valid = false
		}
	}
	flag := func(option string) {
		switch option {
		case "require-parameter":
			d.requires = true
		case "no-files":
			d.noFiles = true
		case "force-files":
			d.forceFiles = true
		case "exclusive":
			d.requires, d.noFiles = true, true
		case "keep-order":
		default: // erase, help
			d.valid = false
		}
	}
	var operands []fishWord
	for i := 0; i < len(words) && d.valid; i++ {
		w := words[i]
		if w.dynamic && !strings.HasPrefix(w.text, "-") {
			return d, false
		}
		next := func() (fishWord, bool) {
			if i+1 >= len(words) {
				return fishWord{}, false
			}
			i++
			return words[i], true
		}
		switch {
		case strings.HasPrefix(w.text, "--") && len(w.text) > 2:
			name, value, attached := strings.Cut(w.text[2:], "=")
			if slices.Contains(mapValues(fishOptionArgument), name) {
				if !attached {
					v, ok := next()
					if !ok {
						return d, false
					}
					set(name, v)
				} else {
					set(name, fishWord{text: value, dynamic: w.dynamic})
				}
			} else if slices.Contains(mapValues(fishSwitch), name) && !attached {
				flag(name)
			} else {
				return d, false
			}
		case strings.HasPrefix(w.text, "-") && len(w.text) > 1:
			for j := 1; j < len(w.text); j++ {
				letter := w.text[j : j+1]
				if option, ok := fishOptionArgument[letter]; ok {
					if j+1 < len(w.text) {
						set(option, fishWord{text: w.text[j+1:], dynamic: w.dynamic})
					} else if v, ok := next(); ok {
						set(option, v)
					} else {
						return d, false
					}
					break
				}
				if option, ok := fishSwitch[letter]; ok {
					flag(option)
					continue
				}
				return d, false
			}
		default:
			operands = append(operands, w)
		}
	}
	if d.command == "" && len(operands) == 1 {
		d.command = operands[0].text
	} else if len(operands) != 0 {
		return d, false
	}
	return d, d.valid && d.command != ""
}

func mapValues(m map[string]string) []string {
	values := make([]string, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

func (d fishDecl) apply(spec *Spec) {
	complete := ""
	switch {
	case d.forceFiles:
		complete = "file"
	case d.noFiles:
		complete = "none"
	}
	if len(d.shorts)+len(d.longs)+len(d.olds) == 0 {
		if len(spec.Args) == 0 {
			spec.Args = []Arg{{Name: "argument", Variadic: true, Optional: true}}
		}
		arg := &spec.Args[0]
		for _, choice := range d.arguments {
			if !slices.Contains(arg.Choices, choice) {
				arg.Choices = append(arg.Choices, choice)
			}
		}
		if complete != "" && arg.Complete != "file" {
			arg.Complete = complete
		}
		return
	}
	f := Flag{Help: d.description}
	for _, s := range d.shorts {
		f.Names = append(f.Names, "-"+s)
	}
	for _, l := range d.longs {
		f.Names = append(f.Names, "--"+l)
	}
	for _, o := range d.olds {
		f.Names = append(f.Names, "-"+o)
	}
	if d.requires || len(d.arguments) != 0 {
		f.Value = "value"
		f.Optional = !d.requires
		f.Choices = d.arguments
		if len(d.arguments) == 0 {
			f.Complete = complete
		}
	}
	mergeInto(spec, Spec{Flags: []Flag{f}})
}
