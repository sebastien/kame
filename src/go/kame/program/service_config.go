package program

import (
	"kame/core"
	"kame/diagnostic"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type serviceConfigField struct {
	Value     core.Value
	Found     bool
	Duplicate bool
}

func serviceField(record core.Value, name string) serviceConfigField {
	var result serviceConfigField
	if record.Kind != core.Record {
		return result
	}
	for i := range record.Record {
		if record.Record[i].Key != name {
			continue
		}
		if result.Found {
			result.Duplicate = true
		} else {
			result.Value, result.Found = record.Record[i].Value, true
		}
	}
	return result
}

func serviceAllowedField(group string, name string) bool {
	switch group {
	case "root":
		return name == "shell" || name == "env" || name == "ready" || name == "health" || name == "restart" || name == "stop" || name == "log-bytes"
	case "ready":
		return name == "argv" || name == "interval-ms" || name == "timeout-ms"
	case "health":
		return name == "argv" || name == "interval-ms" || name == "failures"
	case "restart":
		return name == "attempts" || name == "backoff-ms"
	case "stop":
		return name == "grace-ms"
	default:
		return false
	}
}

func validateServiceRecord(a mem.Allocator, record core.Value, group string, span diagnostic.Span) diagnostic.Diagnostic {
	if record.Kind != core.Record {
		return failureAt(a, "EXPR_INVALID", span, group+" service settings must be a record")
	}
	for i := range record.Record {
		field := record.Record[i]
		if !serviceAllowedField(group, field.Key) {
			return failureAt(a, "EXPR_INVALID", span, "unknown "+group+" service setting: "+field.Key)
		}
		for j := 0; j < i; j++ {
			if record.Record[j].Key == field.Key {
				return failureAt(a, "EXPR_INVALID", span, "duplicate "+group+" service setting: "+field.Key)
			}
		}
	}
	return diagnostic.Diagnostic{}
}

func serviceInteger(a mem.Allocator, record core.Value, name string, fallback int64, minimum int64, maximum int64, span diagnostic.Span, result *int64) diagnostic.Diagnostic {
	field := serviceField(record, name)
	if field.Duplicate {
		return failureAt(a, "EXPR_INVALID", span, "duplicate service setting: "+name)
	}
	if !field.Found {
		*result = fallback
		return diagnostic.Diagnostic{}
	}
	if field.Value.Kind != core.Int || field.Value.Int < minimum || field.Value.Int > maximum {
		return failureAt(a, "EXPR_INVALID", span, "service setting "+name+" is outside its allowed integer range")
	}
	*result = field.Value.Int
	return diagnostic.Diagnostic{}
}

func serviceArgv(a mem.Allocator, record core.Value, span diagnostic.Span, argv *[]string) diagnostic.Diagnostic {
	field := serviceField(record, "argv")
	if field.Duplicate || !field.Found || field.Value.Kind != core.List || len(field.Value.List) == 0 || len(field.Value.List) > 128 {
		return failureAt(a, "EXPR_INVALID", span, "service probe argv must be a nonempty list of at most 128 strings")
	}
	var result []string
	total := 0
	for i := range field.Value.List {
		value := field.Value.List[i]
		if value.Kind != core.String || value.Text == "" || strings.IndexByte(value.Text, 0) >= 0 {
			freeStrings(a, result)
			return failureAt(a, "EXPR_INVALID", span, "service probe argv requires nonempty strings without NUL")
		}
		total += len(value.Text)
		if total > 16384 {
			freeStrings(a, result)
			return failureAt(a, "EXPR_INVALID", span, "service probe argv exceeds 16384 bytes")
		}
		result = slices.Append(a, result, cloneText(a, value.Text))
	}
	*argv = result
	return diagnostic.Diagnostic{}
}

func parseServiceConfig(a mem.Allocator, metadata core.Value, span diagnostic.Span, config *ServiceConfig) diagnostic.Diagnostic {
	*config = ServiceConfig{
		ReadyInterval: 100, ReadyTimeout: 15000,
		HealthInterval: 1000, HealthFailures: 3,
		RestartBackoff: 250, StopGrace: 5000, LogBytes: 65536,
	}
	if metadata.Kind == core.Nil {
		return diagnostic.Diagnostic{}
	}
	if d := validateServiceRecord(a, metadata, "root", span); d.Code != "" {
		return d
	}
	ready := serviceField(metadata, "ready")
	if ready.Duplicate {
		return failureAt(a, "EXPR_INVALID", span, "duplicate service setting: ready")
	}
	if ready.Found {
		if d := validateServiceRecord(a, ready.Value, "ready", span); d.Code != "" {
			return d
		}
		d := serviceArgv(a, ready.Value, span, &config.ReadyArgv)
		if d.Code != "" {
			config.Free(a)
			return d
		}
		d = serviceInteger(a, ready.Value, "interval-ms", 100, 10, 60000, span, &config.ReadyInterval)
		if d.Code != "" {
			config.Free(a)
			return d
		}
		d = serviceInteger(a, ready.Value, "timeout-ms", 15000, 1, 3600000, span, &config.ReadyTimeout)
		if d.Code != "" {
			config.Free(a)
			return d
		}
	}
	health := serviceField(metadata, "health")
	if health.Duplicate {
		config.Free(a)
		return failureAt(a, "EXPR_INVALID", span, "duplicate service setting: health")
	}
	if health.Found {
		if d := validateServiceRecord(a, health.Value, "health", span); d.Code != "" {
			config.Free(a)
			return d
		}
		d := serviceArgv(a, health.Value, span, &config.HealthArgv)
		if d.Code != "" {
			config.Free(a)
			return d
		}
		d = serviceInteger(a, health.Value, "interval-ms", 1000, 10, 60000, span, &config.HealthInterval)
		if d.Code != "" {
			config.Free(a)
			return d
		}
		d = serviceInteger(a, health.Value, "failures", 3, 1, 100, span, &config.HealthFailures)
		if d.Code != "" {
			config.Free(a)
			return d
		}
	}
	restart := serviceField(metadata, "restart")
	if restart.Duplicate {
		config.Free(a)
		return failureAt(a, "EXPR_INVALID", span, "duplicate service setting: restart")
	}
	if restart.Found {
		if d := validateServiceRecord(a, restart.Value, "restart", span); d.Code != "" {
			config.Free(a)
			return d
		}
		d := serviceInteger(a, restart.Value, "attempts", 0, 0, 20, span, &config.RestartAttempts)
		if d.Code != "" {
			config.Free(a)
			return d
		}
		d = serviceInteger(a, restart.Value, "backoff-ms", 250, 0, 60000, span, &config.RestartBackoff)
		if d.Code != "" {
			config.Free(a)
			return d
		}
	}
	stop := serviceField(metadata, "stop")
	if stop.Duplicate {
		config.Free(a)
		return failureAt(a, "EXPR_INVALID", span, "duplicate service setting: stop")
	}
	if stop.Found {
		if d := validateServiceRecord(a, stop.Value, "stop", span); d.Code != "" {
			config.Free(a)
			return d
		}
		d := serviceInteger(a, stop.Value, "grace-ms", 5000, 0, 60000, span, &config.StopGrace)
		if d.Code != "" {
			config.Free(a)
			return d
		}
	}
	d := serviceInteger(a, metadata, "log-bytes", 65536, 0, 1048576, span, &config.LogBytes)
	if d.Code != "" {
		config.Free(a)
		return d
	}
	return diagnostic.Diagnostic{}
}
