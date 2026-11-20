package main

import (
	"errors"
	"fmt"
	"io"
	"kame/internal/mem"
	"kame/lang/script"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Values stay textual at the command/file boundary; the registry validates
// them before either session state or persistent defaults can change.
type replOption struct {
	name, description, defaultValue, environment string
	choices                                      []string
	min, max                                     int64
}

var replOptionRegistry = []replOption{
	{name: "history-max-age-days", description: "retention in 24-hour days", defaultValue: "15", environment: "KASH_HISTORY_MAX_AGE_DAYS", min: 1, max: int64((1<<63 - 1) / (24 * time.Hour))},
	{name: "history-max-bytes", description: "persisted bytes per history scope", defaultValue: "1048576", environment: "KASH_HISTORY_MAX_BYTES", min: 1, max: int64(int(^uint(0)>>1) / 2)},
	{name: "history-scope", description: "history recall view", defaultValue: "local", environment: "KASH_HISTORY_SCOPE", choices: []string{"local", "global"}},
}

func (o replOption) allowed() string {
	if len(o.choices) != 0 {
		return strings.Join(o.choices, "|")
	}
	return fmt.Sprintf("%d..%d", o.min, o.max)
}

func (o replOption) validate(value string) (string, error) {
	if len(o.choices) != 0 {
		if slices.Contains(o.choices, value) {
			return value, nil
		}
	} else if n, err := strconv.ParseInt(value, 10, 64); err == nil && n >= o.min && n <= o.max {
		return strconv.FormatInt(n, 10), nil
	}
	return "", fmt.Errorf("%s expects %s", o.name, o.allowed())
}

func findReplOption(name string) (replOption, bool) {
	for _, option := range replOptionRegistry {
		if option.name == name {
			return option, true
		}
	}
	return replOption{}, false
}

type replOptions struct {
	path          string
	local, global map[string]string
}

func loadReplOptions(out io.Writer) *replOptions {
	o := &replOptions{local: make(map[string]string), global: make(map[string]string)}
	config, err := os.UserConfigDir()
	if err == nil {
		o.path = filepath.Join(config, "kash", "options")
		o.global, err = readReplOptions(o.path)
	}
	if err != nil {
		fmt.Fprintln(out, "kash: option: cannot load global defaults:", err)
	}
	return o
}

func (o *replOptions) value(option replOption) (string, string) {
	if value, ok := o.local[option.name]; ok {
		return value, "local"
	}
	if value, ok := o.global[option.name]; ok {
		return value, "global"
	}
	if value, set := os.LookupEnv(option.environment); set {
		if value, err := option.validate(value); err == nil {
			return value, "environment"
		}
	}
	return option.defaultValue, "default"
}

func readReplOptions(path string) (map[string]string, error) {
	values := make(map[string]string)
	if err := replOptionsDirectory(filepath.Dir(path)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}
		return nil, err
	}
	file, err := historyOpen(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, errors.New("cannot safely open options file")
	}
	defer file.Close()
	const maxSize = 64 << 10
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil || len(data) > maxSize || !utf8.Valid(data) {
		return nil, errors.New("options file is unreadable, invalid UTF-8, or exceeds 64 KiB")
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		option, known := findReplOption(name)
		if !ok || !known {
			return nil, fmt.Errorf("invalid option at line %d", i+1)
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("duplicate option at line %d", i+1)
		}
		value, err = option.validate(value)
		if err != nil {
			return nil, fmt.Errorf("invalid option value at line %d", i+1)
		}
		values[name] = value
	}
	return values, nil
}

func replOptionsDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("unsafe options directory")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe options directory owner")
	}
	return nil
}

func (o *replOptions) save(name, value string) error {
	if o.path == "" {
		return errors.New("user configuration directory unavailable")
	}
	parent := filepath.Dir(o.path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := replOptionsDirectory(parent); err != nil {
		return err
	}
	lock, err := historyOpen(o.path+".lock", true)
	if err != nil {
		return errors.New("cannot safely open options lock")
	}
	defer lock.Close()
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN || time.Now().After(deadline) {
			return errors.New("options lock unavailable")
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	values, err := readReplOptions(o.path)
	if err != nil {
		return err
	}
	values[name] = value
	var data strings.Builder
	for _, option := range replOptionRegistry {
		if value, ok := values[option.name]; ok {
			fmt.Fprintf(&data, "%s: %s\n", option.name, value)
		}
	}
	if err := historyReplace(o.path, []byte(data.String())); err != nil {
		return err
	}
	o.global = values
	return nil
}

func (o *replOptions) command(text string, out, errOut io.Writer) (bool, int) {
	fields := strings.Fields(text)
	if len(fields) == 0 || fields[0] != "option" {
		return false, 0
	}
	fail := func(err error) (bool, int) {
		fmt.Fprintln(errOut, "option:", err)
		return true, 2
	}
	parsed := script.ParseKashSessionText(mem.System, "<option>", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		return fail(errors.New(parsed.Diagnostics[0].Message))
	}
	var command string
	for _, item := range parsed.Items {
		if item.Kind == script.Comment {
			continue
		}
		if item.Kind != script.SessionCommand || command != "" {
			return fail(errors.New("expected one whole session entry"))
		}
		command = item.Text
	}
	if command == "" {
		return fail(errors.New("expected one whole session entry"))
	}
	fields = strings.Fields(command)
	fields = fields[1:]
	global := false
	if len(fields) != 0 {
		switch fields[0] {
		case "-g", "--global":
			global = true
			fields = fields[1:]
		case "-l", "--local":
			fields = fields[1:]
		}
	}
	if len(fields) > 2 || len(fields) != 0 && strings.HasPrefix(fields[0], "-") {
		return fail(errors.New("expected option [-l|--local|-g|--global] [NAME [VALUE]]"))
	}
	options := replOptionRegistry
	if len(fields) != 0 {
		option, ok := findReplOption(fields[0])
		if !ok {
			return fail(errors.New("unknown option"))
		}
		options = []replOption{option}
	}
	if len(fields) == 2 {
		value, err := options[0].validate(fields[1])
		if err != nil {
			return fail(err)
		}
		if global {
			if err := o.save(options[0].name, value); err != nil {
				return fail(err)
			}
		} else {
			o.local[options[0].name] = value
		}
		return true, 0
	}
	for _, option := range options {
		value, source := o.value(option)
		if global {
			var set bool
			value, set = o.global[option.name]
			if !set {
				continue
			}
			source = "global"
		}
		fmt.Fprintf(out, "%s: %s (%s; %s) # %s\n", option.name, value, source, option.allowed(), option.description)
	}
	return true, 0
}
