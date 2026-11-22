package usage

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"io"
	"regexp"
	"strings"
)

// maxManBytes bounds a decompressed page; real pages are far smaller.
const maxManBytes = 8 << 20

// DecodeMan returns the roff text of a page, decompressing gzip and bzip2.
func DecodeMan(data []byte) (string, error) {
	var r io.Reader
	switch {
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		z, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		r = z
	case bytes.HasPrefix(data, []byte("BZh")):
		r = bzip2.NewReader(bytes.NewReader(data))
	case bytes.HasPrefix(data, []byte{0xfd, '7', 'z', 'X', 'Z'}), bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return "", errors.New("unsupported man page compression")
	default:
		return string(data), nil
	}
	text, err := io.ReadAll(io.LimitReader(r, maxManBytes+1))
	if err != nil {
		return "", err
	}
	if len(text) > maxManBytes {
		return "", errors.New("man page too large")
	}
	return string(text), nil
}

// ManRedirect returns the target of a page that only includes another
// (".so man1/other.1"), or "".
func ManRedirect(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isRoffComment(line) {
			continue
		}
		if target, ok := strings.CutPrefix(line, ".so "); ok {
			return strings.TrimSpace(target)
		}
		return ""
	}
	return ""
}

func isRoffComment(line string) bool {
	return strings.HasPrefix(line, `.\"`) || strings.HasPrefix(line, `'\"`) || strings.HasPrefix(line, `.\#`) || line == "."
}

// ParseMan extracts the command summary and options of a man(7) or mdoc(7)
// page. It only reads option lists: tagged items whose tag starts with a dash.
// The spec is named by the command words that start the synopsis, such as
// "git commit".
func ParseMan(text string) Spec {
	var spec Spec
	var item []string // tag words, for an item being read
	var tag string
	var description []string
	section, name := "", "" // name: the mdoc .Nm of the NAME section
	inXo := false
	flush := func() {
		if tag != "" {
			if flags := parseOptionTag(tag); len(flags) != 0 {
				help := firstSentence(description)
				for _, f := range flags {
					f.Help = help
					if spec.flag(f.Names[0]) == nil {
						spec.Flags = append(spec.Flags, f)
					}
				}
			}
		}
		tag, description, item = "", nil, nil
	}
	pendingTag := false // .TP: the next line is the tag
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		for strings.HasSuffix(line, `\`) && !strings.HasSuffix(line, `\\`) && i+1 < len(lines) {
			i++
			line = line[:len(line)-1] + lines[i]
		}
		if isRoffComment(strings.TrimSpace(line)) {
			continue
		}
		if inXo {
			if macro, args := roffMacro(line); macro == "Xc" {
				inXo = false
				tag = mdocTag(strings.Join(item, " "), name)
			} else if macro != "" {
				item = append(item, macro+" "+args)
			}
			continue
		}
		macro, args := roffMacro(line)
		if section == "SYNOPSIS" && spec.Name == "" {
			switch {
			case macro == "Nm" && strings.TrimSpace(args) == "":
				spec.Name = name
			case macro == "" || fontMacro(macro) || macro == "Nm":
				spec.Name = synopsisCommand(roffText(line))
			}
		}
		if pendingTag && macro != "" && !fontMacro(macro) && macro != "Fl" {
			// A request such as .sp or .ns before the tag line.
			if macro == "TP" || macro == "SH" || macro == "Sh" || macro == "SS" {
				pendingTag = false
			} else {
				continue
			}
		}
		if pendingTag {
			pendingTag = false
			tag = roffText(line)
			continue
		}
		switch macro {
		case "":
			text := roffText(line)
			// DocBook and asciidoc pages: an option line, then .RS and its description.
			if strings.HasPrefix(text, "-") && (tag == "" || len(description) != 0) && (nextMacro(lines, i+1) == "RS" || nextMacro(lines, i+1) == "INDENT") {
				flush()
				tag = text
				continue
			}
			if tag != "" || section == "NAME" {
				description = append(description, text)
			}
		case "SH", "Sh":
			flush()
			section = strings.ToUpper(strings.Trim(roffText(args), `" `))
		case "SS", "Ss", "PP", "LP", "P", "El", "Bl", "RE", "UNINDENT":
			flush()
		case "RS", "INDENT":
			// An item's indented description continues the item.
		case "TP":
			flush()
			pendingTag = true
		case "IP", "HP":
			fields := roffArgs(args)
			if len(fields) == 0 && macro == "IP" {
				break // an indented paragraph within the item
			}
			flush()
			if len(fields) != 0 {
				tag = roffText(fields[0])
			} else {
				pendingTag = true // .HP: the next line is the tag
			}
		case "It":
			flush()
			if strings.TrimSpace(args) == "Xo" || strings.HasSuffix(strings.TrimSpace(args), " Xo") {
				inXo = true
				item = []string{strings.TrimSuffix(strings.TrimSpace(args), "Xo")}
				continue
			}
			tag = mdocTag(args, name)
		case "Nm":
			if name == "" && section == "NAME" {
				name = strings.TrimSpace(roffText(args))
			}
			if tag != "" {
				description = append(description, mdocTag("Nm "+args, name))
			}
		case "Nd":
			spec.Help = roffText(args)
		default:
			if fontMacro(macro) || mdocInline(macro) {
				text := roffFontLine(macro, args)
				if macro != "" && mdocInline(macro) {
					text = mdocTag(macro+" "+args, name)
				}
				if tag != "" || section == "NAME" {
					description = append(description, text)
				}
			}
		}
		if section == "NAME" && spec.Help == "" && len(description) != 0 {
			if _, summary, ok := strings.Cut(strings.Join(description, " "), " - "); ok {
				spec.Help = strings.TrimSpace(summary)
			}
		}
	}
	flush()
	return spec
}

var commandWord = regexp.MustCompile(`^[a-z0-9][a-z0-9_.+-]*$`)

// synopsisCommand returns the leading command words of a synopsis line.
func synopsisCommand(line string) string {
	var words []string
	for _, word := range strings.Fields(line) {
		if !commandWord.MatchString(word) {
			break
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

func fontMacro(m string) bool {
	switch m {
	case "B", "I", "BR", "RB", "BI", "IB", "IR", "RI", "SM", "SB", "OP":
		return true
	}
	return false
}

func mdocInline(m string) bool {
	switch m {
	case "Fl", "Ar", "Cm", "Ic", "Li", "Sy", "Em", "Pa", "Va", "Op", "Oo", "Oc", "Dq", "Ql", "Xr", "Nm", "Ev", "Ns":
		return true
	}
	return false
}

// roffMacro splits a control line into its macro name and argument text.
func roffMacro(line string) (string, string) {
	if line == "" || line[0] != '.' && line[0] != '\'' {
		return "", ""
	}
	rest := strings.TrimLeft(line[1:], " \t")
	name, args, _ := strings.Cut(rest, " ")
	return name, strings.TrimSpace(args)
}

// roffArgs splits macro arguments, honoring double quotes.
func roffArgs(args string) []string {
	var out []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		if args[0] == '"' {
			end := strings.IndexByte(args[1:], '"')
			if end < 0 {
				out = append(out, args[1:])
				break
			}
			out = append(out, args[1:end+1])
			args = args[end+2:]
			continue
		}
		word, rest, _ := strings.Cut(args, " ")
		out = append(out, word)
		args = rest
	}
	return out
}

// roffFontLine renders a font macro line: alternating macros join their
// arguments without spaces, single-font macros with spaces.
func roffFontLine(macro, args string) string {
	words := roffArgs(args)
	if len(macro) == 2 && macro != "SM" && macro != "SB" && macro != "OP" {
		return roffText(strings.Join(words, ""))
	}
	if macro == "OP" {
		return "[" + roffText(strings.Join(words, " ")) + "]"
	}
	return roffText(strings.Join(words, " "))
}

var roffSpecial = map[string]string{
	"aq": "'", "dq": "\"", "lq": "\"", "rq": "\"", "oq": "'", "cq": "'", "em": "-", "en": "-",
	"hy": "-", "mi": "-", "bu": "*", "ga": "`", "ti": "~", "ha": "^", "rs": "\\", "co": "(c)", "lh": "", "rh": "",
}

// roffText removes escapes and font changes from text.
func roffText(text string) string {
	if macro, args := roffMacro(text); macro != "" {
		if fontMacro(macro) {
			return roffFontLine(macro, args)
		}
		text = args
	}
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != '\\' || i+1 >= len(text) {
			b.WriteByte(c)
			continue
		}
		i++
		switch e := text[i]; e {
		case 'e':
			b.WriteByte('\\')
		case ' ', '~', '0':
			b.WriteByte(' ')
		case '&', '|', '^', ',', '/', '%', 'c', ':', ')':
		case '"':
			return strings.TrimSpace(b.String())
		case '(':
			if i+2 < len(text) {
				b.WriteString(roffSpecial[text[i+1:i+3]])
				i += 2
			}
		case '[':
			if end := strings.IndexByte(text[i:], ']'); end >= 0 {
				b.WriteString(roffSpecial[text[i+1:i+end]])
				i += end
			}
		case 'f':
			i += roffEscapeArgument(text, i+1)
		case '*', 'n':
			i += roffEscapeArgument(text, i+1)
		case 's':
			if i+1 < len(text) && (text[i+1] == '+' || text[i+1] == '-') {
				i++
			}
			if i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9' {
				i++
			}
		case 'X', 'h', 'v', 'w', 'o', 'l', 'L', 'D', 'k', 'Z', 'A', 'b':
			if i+1 < len(text) && text[i+1] == '\'' {
				if end := strings.IndexByte(text[i+2:], '\''); end >= 0 {
					i += end + 2
				}
			}
		default:
			b.WriteByte(e)
		}
	}
	return strings.TrimSpace(b.String())
}

// roffEscapeArgument measures the name after \f, \* or \n: one character,
// (xx, or [name].
func roffEscapeArgument(text string, i int) int {
	if i >= len(text) {
		return 0
	}
	switch text[i] {
	case '(':
		return min(3, len(text)-i)
	case '[':
		if end := strings.IndexByte(text[i:], ']'); end >= 0 {
			return end + 1
		}
	}
	return 1
}

// mdocTag renders the macros of an mdoc .It line as option text.
func mdocTag(args, name string) string {
	var b strings.Builder
	space, optional := false, 0
	words := roffArgs(args)
	for i := 0; i < len(words); i++ {
		word := words[i]
		switch word {
		case "Nm":
			if i+1 < len(words) && !mdocMacros[words[i+1]] {
				continue // the name follows as text
			}
			word = name
		case "Xr":
			if i+2 < len(words) {
				word = words[i+1] + "(" + words[i+2] + ")"
				i += 2
			}
		}
		switch word {
		case "Fl":
			if space {
				b.WriteByte(' ')
			}
			b.WriteByte('-')
			space = false
			continue
		case "Ns":
			space = false
			continue
		case "Op", "Oo":
			if space {
				b.WriteByte(' ')
			}
			b.WriteByte('[')
			optional++
			space = false
			continue
		case "Oc":
			b.WriteByte(']')
			optional--
			continue
		case "":
			continue
		}
		if mdocMacros[word] {
			continue
		}
		text := roffText(word)
		if space && !strings.Contains(",|.;:)", text) {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		space = true
	}
	for ; optional > 0; optional-- {
		b.WriteByte(']')
	}
	return b.String()
}

var mdocMacros = map[string]bool{}

func init() {
	for _, m := range strings.Fields("Ad An Ar Aq At Bq Brq Bx Cd Cm Dq Dv Em Er Ev Fa Fn Ft Ic Li Lk Mt No Pa Pq Ql Qq Sq Sx Sy Tn Ux Va Xo Xc") {
		mdocMacros[m] = true
	}
}

// nextMacro returns the macro of the next nonblank line from i.
func nextMacro(lines []string, i int) string {
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" && !isRoffComment(strings.TrimSpace(lines[i])) {
			macro, _ := roffMacro(lines[i])
			return macro
		}
	}
	return ""
}

var optionName = regexp.MustCompile(`^--?[A-Za-z0-9?@#][A-Za-z0-9_.+?@#-]*`)

// parseOptionTag reads an item tag such as "-a, --all", "-w, --width=COLS",
// "--color[=WHEN]" or "-B bind_interface". It returns one flag holding every
// spelling, or none when the tag does not start with an option.
func parseOptionTag(tag string) []Flag {
	tag = strings.TrimRight(strings.TrimSpace(tag), ":")
	if !strings.HasPrefix(tag, "-") {
		return nil
	}
	var f Flag
	for _, part := range splitSpellings(strings.ReplaceAll(tag, " / ", ", ")) {
		part = strings.TrimSpace(part)
		name := optionName.FindString(part)
		if name == "" || name == "-" || name == "--" {
			break
		}
		f.Names = append(f.Names, name)
		rest := strings.TrimSpace(part[len(name):])
		switch {
		case rest == "":
		case strings.HasPrefix(rest, "[="):
			f.Value, f.Optional = placeholder(rest[2:]), true
		case strings.HasPrefix(rest, "="):
			f.Value, f.Optional = placeholder(rest[1:]), false
		case strings.HasPrefix(rest, "["):
			if f.Value == "" {
				f.Value, f.Optional = placeholder(rest[1:]), true
			}
		default:
			f.Value, f.Optional = placeholder(rest), false
		}
	}
	if len(f.Names) == 0 {
		return nil
	}
	return []Flag{f}
}

// splitSpellings splits "-a, --all" and "-v | --verbose" outside brackets.
func splitSpellings(tag string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(tag); i++ {
		switch tag[i] {
		case '[', '<', '{':
			depth++
		case ']', '>', '}':
			depth--
		case ',', '|':
			if depth == 0 {
				parts = append(parts, tag[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, tag[start:])
}

// placeholder names a value from text such as "<name>=<value>" or "FILE".
func placeholder(text string) string {
	word, _, _ := strings.Cut(strings.TrimSpace(text), " ")
	if word = placeholderWord.FindString(word); word == "" {
		return "value"
	}
	return word
}

var placeholderWord = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_-]*`)

// sentenceEnd finds a period ending a word, before a capitalized word.
var sentenceEnd = regexp.MustCompile(`[A-Za-z0-9)"'][.;:] [A-Z(]`)

// firstSentence returns the first sentence of a description, bounded.
// A line ending in a period also ends a sentence, as in roff.
func firstSentence(lines []string) string {
	for i, line := range lines {
		if line = strings.TrimSpace(line); len(line) > 1 && strings.HasSuffix(line, ".") && !strings.HasSuffix(line, "..") {
			lines = lines[:i+1]
			break
		}
	}
	text := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	if m := sentenceEnd.FindStringIndex(text); m != nil {
		text = text[:m[0]+1]
	}
	if len(text) > 1 && text[len(text)-1] == '.' && text[len(text)-2] != ' ' && text[len(text)-2] != '.' {
		text = text[:len(text)-1]
	}
	if len(text) > 120 {
		cut := strings.LastIndexByte(text[:120], ' ')
		if cut < 60 {
			cut = 120
		}
		text = text[:cut] + "…"
	}
	return text
}
