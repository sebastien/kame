package cli

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

type CommandInfo struct {
	Name    string
	Usage   string
	Summary string
}

var Commands = [12]CommandInfo{
	{Name: "run", Usage: "kame do run [OPTIONS] INPUT... [-- ARG...]", Summary: "execute ordered source fragments in one session"},
	{Name: "plan", Usage: "kame do plan [OPTIONS] [TARGET...]", Summary: "inspect rule, inputs, outputs, captures, tools, and freshness without execution"},
	{Name: "inputs", Usage: "kame do inputs [--depth N] [OPTIONS] [TARGET]", Summary: "list declared input edges"},
	{Name: "outputs", Usage: "kame do outputs [--depth N] [OPTIONS] [TARGET]", Summary: "list declared output edges"},
	{Name: "span", Usage: "kame do span [--expand] [--depth N] [OPTIONS] [TARGET]", Summary: "separate static and evaluation-dependent resources"},
	{Name: "tools", Usage: "kame do tools [OPTIONS]\nkame do tools check [OPTIONS] TARGETS...", Summary: "list referenced tools or check tools required by selected targets"},
	{Name: "parse", Usage: "kame do parse --lang LANG [FILE]", Summary: "inspect a language AST; omitted FILE reads stdin"},
	{Name: "fmt", Usage: "kame do fmt [--lang LANG] [--indent tabs|spaces] [--indent-width N] [-i | -n] [FILE...]", Summary: "format exact source, replace files atomically, or check differences"},
	{Name: "render", Usage: "kame do render [-c TEXT | FILE] [--define NAME=VALUE]... [--comment STYLE] [--check]", Summary: "render exact document bytes or check template syntax"},
	{Name: "cat", Usage: "kame do cat [OPTIONS] [TARGET]", Summary: "materialize one artifact and print its exact bytes without an added newline"},
	{Name: "cache", Usage: "kame do cache list|clean [-C DIR]", Summary: "inspect or remove managed cache records"},
	{Name: "help", Usage: "kame do help [COMMAND]", Summary: "show the overview or command-specific help"},
}

const GlobalHelp = `  -o, --output MODE       ansi (default), text, or json
      --json              alias for --output json
      --color MODE        auto, always, or never
      --diagnostic-format human|plain
  -h, --help              show help
  -V, --version           show version
`

const BuildHelp = `  -f, --file FILE         select/append source (repeatable in execution)
  -c, --command TEXT      append inline source (repeatable in execution)
  -C, --directory DIR     set working directory
  -j, --jobs N            maximum concurrent nodes (default 1)
  -n, --dry-run           plan/render without effects or processes
      --watch             rebuild tracked inputs on native and WASM hosts
      --force             bypass freshness and cached-task hits
      --verbose           detailed process and dependency events
      --define NAME=VALUE override a declared value with literal text
      --tool NAME=PATH    select a declared executable
      --shell SHELL       recipe shell executable/arguments (repeatable)
      --env NAME=VALUE    replace a process environment entry
      --timeout MS        timeout in milliseconds
      --retry N           retry failed commands
      --log-limit N       retained recipe bytes per stream
      --capture-limit N   captured substitution stdout byte limit
`

// HelpText returns allocator-owned text; free it with mem.System.
func HelpText(topic string) string {
	b := strings.NewBuilder(mem.System)
	if topic == "" {
		b.WriteString("kame - a modern build system in the spirit of GNU Make.\n\nUsage:\n  kame [OPTIONS] [TARGET...]\n  kame [OPTIONS] INPUT... [-- ARG...]\n  kame do COMMAND [OPTIONS] [ARG...]\n\nSource discovery: Makefile.kmk, make.kmk, src/kmk/main.kmk.\nWithout targets, select default; without a source or arguments, show help.\n\nBuild options:\n")
		b.WriteString(BuildHelp)
	} else if topic != "do" {
		for i := range Commands {
			if Commands[i].Name == topic {
				b.WriteString("Usage: ")
				b.WriteString(Commands[i].Usage)
				b.WriteString("\n\n")
				b.WriteString(Commands[i].Summary)
				b.WriteString("\n\n")
				break
			}
		}
		if topic == "run" {
			b.WriteString("-l, --lang km|kmk|kash|expr; --entry NAME selects the preceding source entry.\nFiles/-f and -c compose ordered fragments; inline input defaults to km.\n")
		}
		if topic == "run" || topic == "render" {
			b.WriteString("Capabilities: --allow-read[=ROOTS], --allow-write[=ROOTS], --allow-run[=ROOTS], --allow-env[=NAMES].\n")
		}
		if topic == "parse" || topic == "fmt" {
			b.WriteString("-l, --lang expr|template|rule|script|km|kmk|kash\n")
		}
		if topic == "fmt" {
			b.WriteString("-i, --in-place replaces files atomically; -n, --check lists differences (exit 1).\n--indent-width N accepts 1..16; --comment STYLE selects template comment syntax.\n")
		}
		if topic == "render" {
			b.WriteString("--define NAME=VALUE supplies literal payload; template reads need an explicit grant.\n--comment STYLE selects template comment syntax; --check emits no document.\n")
		}
		if topic == "inputs" || topic == "outputs" || topic == "span" {
			b.WriteString("--depth 0: no edges; 1: direct edges; -1: unlimited. --expand is span-only.\n")
		}
		if topic == "plan" || topic == "cat" || topic == "tools" || topic == "inputs" || topic == "outputs" || topic == "span" {
			b.WriteString("Source: -f FILE or -c TEXT; -C DIR, --define NAME=VALUE, --tool NAME=PATH.\nWith no target, select default. Unknown targets fail without execution.\n")
		}
	}
	if topic == "" || topic == "do" || topic == "help" {
		b.WriteString("\nCommands:\n")
		for i := range Commands {
			b.WriteString("  ")
			b.WriteString(Commands[i].Name)
			b.WriteString("  ")
			b.WriteString(Commands[i].Summary)
			b.WriteString("\n")
		}
	}
	b.WriteString("\nPresentation options (all commands):\n")
	b.WriteString(GlobalHelp)
	b.WriteString("\nHuman output is the default. Exact artifact/source/document bytes are never decorated.\nUse --output json for machine data: execution is JSON Lines; AST/graph/tool/cache lists are JSON documents.\n\nExamples:\n  kame\n  kame -f Build.kmk dist\n  kame do plan --json dist\n  kame do fmt -i Makefile.kmk\n  kame do run --lang expr -c '42'\n\nRun 'kame do COMMAND --help' for command-specific help.\n")
	text := cloneText(b.String())
	b.Free()
	return text
}

func WriteHelp(out io.Writer, topic string, machine bool, styled bool) {
	text := HelpText(topic)
	defer mem.FreeString(mem.System, text)
	if !machine {
		start := 0
		for start < len(text) {
			end := start
			for end < len(text) && text[end] != '\n' {
				end++
			}
			line := text[start:end]
			token := "text.primary"
			if start == 0 || (len(line) != 0 && line[len(line)-1] == ':') {
				token = "heading"
			}
			Style(out, token, line, styled)
			if end < len(text) {
				io.WriteString(out, "\n")
			}
			start = end + 1
		}
		return
	}
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("help")
	e.Str("topic")
	e.Str(topic)
	e.Str("usage")
	e.BeginArray()
	if topic == "" || topic == "do" {
		e.Str("kame [OPTIONS] [TARGET...]")
		e.Str("kame [OPTIONS] INPUT... [-- ARG...]")
		e.Str("kame do COMMAND [OPTIONS] [ARG...]")
	} else {
		for i := range Commands {
			if Commands[i].Name == topic {
				e.Str(Commands[i].Usage)
			}
		}
	}
	e.EndArray()
	e.Str("commands")
	e.BeginArray()
	for i := range Commands {
		e.BeginObject()
		e.Str("name")
		e.Str(Commands[i].Name)
		e.Str("usage")
		e.Str(Commands[i].Usage)
		e.Str("description")
		e.Str(Commands[i].Summary)
		e.EndObject()
	}
	e.EndArray()
	e.Str("options")
	e.Str(GlobalHelp)
	e.Str("examples")
	e.BeginArray()
	e.Str("kame do plan --json default")
	e.Str("kame do run --lang expr -c '42'")
	e.EndArray()
	e.Str("text")
	e.Str(text)
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}
