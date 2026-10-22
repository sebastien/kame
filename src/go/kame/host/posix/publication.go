package posix

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/c"
)

//so:extern nodecay
func km_file_publish(staged string, destination string) c.Int {
	_, _ = staged, destination
	return 3
}

func (h *Host) PublishFile(staged string, destination string) host.PublishResult {
	if core.IsMemoryResourceURIName(staged) || core.IsMemoryResourceURIName(destination) {
		return host.PublishResult{Code: "FS_UNSUPPORTED"}
	}
	source, target := h.hostPath(staged), h.hostPath(destination)
	defer h.freeHostPath(source)
	defer h.freeHostPath(target)
	if source.Error != nil || target.Error != nil {
		return host.PublishResult{Code: "FS_ERR"}
	}
	switch km_file_publish(source.Path, target.Path) {
	case 0:
		return host.PublishResult{Published: true, StagedRemoved: true}
	case 1:
		return host.PublishResult{Code: "FS_EXISTS"}
	case 2:
		return host.PublishResult{Code: "FS_PUBLISHED", Published: true}
	case 3:
		return host.PublishResult{Code: "FS_UNSUPPORTED"}
	}
	return host.PublishResult{Code: "FS_ERR"}
}
