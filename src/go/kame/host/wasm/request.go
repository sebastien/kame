package wasm

import "kame/host"

// ABIRequestPayload selects the primitive payload transported to a WASM host.
func ABIRequestPayload(r host.Request) string {
	switch r.Kind {
	case host.RequestRecordRead, host.RequestRecordClose, host.RequestPublishFile, host.RequestPlugin, host.RequestTimer, host.RequestProcessCancel:
		return host.PayloadText(r.Payload, "data")
	case host.RequestProcess, host.RequestPrepareOutputs:
		if s := host.PayloadText(r.Payload, "data"); s != "" {
			return s
		}
		return host.PayloadText(r.Payload, "script")
	default:
		return host.PayloadPath(r.Payload)
	}
}

// ABIRequestKind maps engine kinds onto the stable public WASM protocol.
func ABIRequestKind(r host.Request) uint32 {
	switch r.Kind {
	case host.RequestReadFile:
		op := host.PayloadText(r.Payload, "op")
		switch op {
		case "resolve-tool":
			return 18
		case "tool-exists":
			return 19
		case "output-exists":
			return 17
		case "file-times":
			return 20
		case "file-content":
			return 27
		case "tool-content":
			return 28
		case "output-content":
			return 29
		case "exists":
			return 7
		case "stat":
			return 5
		case "lstat":
			return 30
		case "readlink":
			return 31
		case "wildcard":
			return 6
		}
		return 1
	case host.RequestProcess:
		if kinds := host.PayloadList(r.Payload, "kinds"); len(kinds) > 0 {
			for _, k := range kinds {
				if k.Text == "each" {
					return 38
				}
			}
			if host.PayloadInt(r.Payload, "failurePolicy") == 1 {
				return 37
			}
			if len(host.PayloadList(r.Payload, "orders")) > 0 {
				return 36
			}
			if len(host.PayloadList(r.Payload, "overflows")) > 0 {
				return 35
			}
			if len(host.PayloadList(r.Payload, "buffers")) > 0 {
				return 34
			}
			for _, edge := range host.PayloadList(r.Payload, "edges") {
				if len(edge.List) == 3 {
					return 33
				}
			}
			return 32
		}
		if host.PayloadText(r.Payload, "op") == "recipe" {
			return 16
		}
		if len(host.PayloadSetups(r.Payload)) > 0 || host.PayloadText(r.Payload, "input") != "" || host.PayloadText(r.Payload, "output") != "" {
			return 15
		}
		if len(host.PayloadStages(r.Payload)) > 0 {
			return 14
		}
		if len(host.PayloadArgv(r.Payload)) > 0 {
			return 13
		}
		return 3
	case host.RequestWallTime:
		return 8
	case host.RequestMonotonicTime:
		return 9
	case host.RequestCacheGet:
		return 10
	case host.RequestCachePut:
		return 11
	case host.RequestCacheDelete:
		return 12
	case host.RequestPrepareOutputs:
		return 21
	case host.RequestTimer:
		return 22
	case host.RequestProcessCancel:
		return 23
	case host.RequestCacheLock:
		return 24
	case host.RequestCacheUnlock:
		return 25
	case host.RequestPlugin:
		return 26
	case host.RequestPublishFile:
		return 39
	case host.RequestRecordRead:
		return 40
	case host.RequestRecordClose:
		return 41
	case host.RequestLoadSource:
		return 42
	default:
		return uint32(r.Kind)
	}
}
